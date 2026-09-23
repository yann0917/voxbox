// 混音引擎（方案 A）：双 AudioBuffer 实时预览，是后端 audio/mix 导出链的前端孪生。
// 包络 automation 与后端 volume 表达式同语义——internal/provider/audiotool/envelope.go
// 是语义权威（docs/plans/2026-09-19-mixer-workbench.md §6.3/§6.4）：段内振幅域插值
// （linearRamp 与后端编译表达式同为振幅线性，envValueAt 亦同）：
//   vocal_env 非空 → 包络完全替代 vocal_gain（后端二选一编译，不做乘法叠加）；
//   vocal_env 空   → 恒 vocal_gain。
// 歌曲场景内存前提：两轨 decodeAudioData 常驻（4 分钟约 170MB），dispose 时 ctx.close() 一并释放。

/** 包络单点：[秒, dB]——与后端 vocal_env 同一份数据形状（plan §6.4，前端编辑器共用）。 */
export type EnvPoint = [t: number, db: number];

/** dB → 线性增益（推子与包络点统一换算）。 */
export const dbToLin = (db: number) => Math.pow(10, db / 20);

/**
 * 分段线性取值——与后端 compileEnvExpr 的嵌套 if 逐点同语义（语义锚点，编辑器画曲线复用）。
 * 实现按「找第一个满足 t < T_k 的点」扫描，与后端 lt(t,T_k) 条件链一一对应：
 *   命中 k=0 → 首点值（首点前=首点值）；命中 k≥1 → 段 seg(P_{k-1}→P_k) 取值；
 *   同刻段退化为常量 G_k（硬跳变落跳后值）；全不命中 → 末点值（末点后=末点值）。
 * 段内在振幅域插值后折回 dB：linearRamp automation 与后端编译表达式都是振幅线性
 * （终审收口），故恒有 dbToLin(envValueAt(…))=振幅真值——seek 锚点与导出逐点一致；
 * 若在 dB 域取中值，深垫音段（-16→-60）中点会偏差 ~16dB，直到下一 ramp 事件才被纠正。
 * 注意段判定是左闭右开（t < T_k 才进入前一段的线性式之外）：恰落在同刻双点时刻时
 * 返回跳后值而非跳前值——跳变点是后端语义，画曲线与预览必须同此。
 */
export function envValueAt(pts: EnvPoint[], t: number): number {
  if (pts.length === 0) return 0; // 空包络由调用方按恒 vocal_gain 处理，这里仅兜底
  for (let k = 0; k < pts.length; k++) {
    if (t < pts[k][0]) {
      if (k === 0) return pts[0][1]; // 首点前=首点值
      const [t1, g1] = pts[k - 1];
      const [t2, g2] = pts[k];
      if (t2 === t1) return g2; // 同刻双点=硬跳变，取跳后值
      const r = (t - t1) / (t2 - t1);
      return 20 * Math.log10(dbToLin(g1) + (dbToLin(g2) - dbToLin(g1)) * r); // 振幅域插值，折回 dB
    }
  }
  return pts[pts.length - 1][1]; // 末点后=末点值
}

/** 同刻双点的先后顺序=跳变方向，必须稳定排序（Array#sort 自 ES2019 起规范保证稳定，与后端 SliceStable 一致）。 */
const sortEnv = (pts: EnvPoint[]) => [...pts].sort((a, b) => a[0] - b[0]);

export class MixerEngine {
  private ctx: AudioContext;
  private music: AudioBuffer;
  private vocal: AudioBuffer;
  private musicGain: GainNode;
  private vocalGain: GainNode;
  private vocalHP: BiquadFilterNode;
  private sources: AudioBufferSourceNode[] = [];
  /** 本次启动的 ctx 时刻；播放中 time = offset + (currentTime - startCtxTime)。 */
  private startCtxTime = 0;
  private offset = 0;
  private _playing = false;
  /** 恒值人声增益（dB），默认与后端 vocal_gain 一致；有包络时仅记录、不被使用。 */
  private vocalGainDB = -16;
  /** 恒值伴奏增益（dB），默认 0dB 直通；setMusicGain 实时下发（预览与导出同值）。 */
  private musicGainDB = 0;
  private env: EnvPoint[] = [];
  private musicMuted = false;
  private vocalMuted = false;
  private ticker?: number;
  /** 播放中的位置回调（约 80ms 一次，UI 进度条订阅用）。 */
  onTick?: (t: number) => void;

  // 私有构造：AudioContext 在 load() 里创建后传入——构造前必须先拿到解码结果，
  // 也避免「创建后解码失败」泄漏一个无人消费的上下文（load 里负责失败回收）。
  private constructor(ctx: AudioContext, music: AudioBuffer, vocal: AudioBuffer) {
    this.ctx = ctx;
    this.music = music;
    this.vocal = vocal;
    // 图拓扑一次性固定：music→musicGain→destination；vocal→vocalHP→vocalGain→destination。
    // 固定拓扑是两处简化的前提：seek 只重建 source 不动连线；低切旁路只调频率不重连。
    this.musicGain = ctx.createGain();
    this.vocalGain = ctx.createGain();
    this.vocalHP = ctx.createBiquadFilter();
    this.vocalHP.type = "highpass";
    this.vocalHP.Q.value = 0.7071; // ffmpeg highpass 默认 Butterworth Q≈0.7071（WebAudio 默认 1 多 ~1dB 截止谐振），预览对齐导出
    this.vocalHP.frequency.value = 120; // 与后端 vocal_highpass 默认一致，实际值随 setVocalHighpass 下发
    this.vocalHP.connect(this.vocalGain).connect(ctx.destination);
    this.musicGain.connect(ctx.destination);
    this.applyGains();
    this.applyEnvelope(0, 0);
  }

  /** 拉取并解码双轨（同源 /api/artifacts/:id/stream，cookie 会话自动携带）。 */
  static async load(musicURL: string, vocalURL: string): Promise<MixerEngine> {
    const ctx = new AudioContext();
    try {
      const [m, v] = await Promise.all(
        [musicURL, vocalURL].map(async (u) => {
          const r = await fetch(u);
          if (!r.ok) throw new Error(`音频加载失败 (${r.status})`);
          return ctx.decodeAudioData(await r.arrayBuffer());
        }),
      );
      return new MixerEngine(ctx, m, v);
    } catch (e) {
      void ctx.close(); // 任一轨拉取/解码失败都要回收上下文，别占着硬件资源
      throw e;
    }
  }

  get duration() {
    return Math.max(this.music.duration, this.vocal.duration); // 短轨播完即静默，总长取两轨最大
  }

  get playing() {
    return this._playing;
  }

  /** 当前播放位置。启动有 30ms 提前量（见 startSources），提前期内仍视为 offset，避免时间回跳。
   *  sources 为空=音频尚未真正调度（play 的 await resume() 启动窗口），位置同样停在 offset——
   *  此时 elapsed 项基于旧锚点是陈值，不能外露（重入窗口的 time 读取安全）。 */
  get time() {
    return this._playing && this.sources.length > 0
      ? Math.min(this.duration, this.offset + Math.max(0, this.ctx.currentTime - this.startCtxTime))
      : this.offset;
  }

  private applyGains() {
    // 静音只动监听（置 0）、不改参数语义；恢复时按当前推子值回来
    this.musicGain.gain.value = this.musicMuted ? 0 : dbToLin(this.musicGainDB);
    this.vocalGain.gain.value = this.vocalMuted ? 0 : dbToLin(this.vocalGainDB); // 无包络时兜底
  }

  /**
   * 包络 automation 重铺：锚定 fromAbs（当前播放位置）与 startCtxTime（本次启动的 ctx 时刻），
   * 事件绝对时刻 = startCtxTime + (点时刻 - fromAbs)。播放中用 (this.time, ctx.currentTime)
   * 重铺时，新算出的绝对时刻与原锚点完全重合——这是 seek/改参/静音都能无缝衔接的原因。
   */
  private applyEnvelope(fromAbs: number, startCtxTime: number) {
    const g = this.vocalGain.gain;
    g.cancelScheduledValues(0); // 清掉全部旧事件（含在播 ramp），整层重铺
    if (this.env.length === 0) {
      g.setValueAtTime(this.vocalMuted ? 0 : dbToLin(this.vocalGainDB), startCtxTime);
      return;
    }
    // 首事件=当前位置的瞬时包络值，其后各点 linearRamp——与后端分段线性一致；
    // 同刻双点会在同一绝对时刻排两个零时长 ramp，即硬跳变（plan §6.4 契约，后端按此编译）。
    g.setValueAtTime(this.vocalMuted ? 0 : dbToLin(envValueAt(this.env, fromAbs)), startCtxTime);
    for (const [t, db] of this.env) {
      if (t <= fromAbs) continue; // 已过的点其值已由首事件覆盖（envValueAt 与跳变语义一致）
      g.linearRampToValueAtTime(this.vocalMuted ? 0 : dbToLin(db), startCtxTime + (t - fromAbs));
    }
  }

  /**
   * AudioBufferSourceNode 是一次性节点且不可暂停：seek/续播一律
   * 「stop 旧 source → 从 offset 新建 → 重铺 automation」（plan §6.3 记录偏移+重建模式）。
   */
  private startSources(fromAbs: number) {
    this.sources.forEach((s) => {
      try {
        s.stop();
      } catch {
        /* 已自然播完的 source 再 stop 会抛，忽略 */
      }
      s.disconnect();
    });
    this.sources = [];
    const t0 = this.ctx.currentTime + 0.03; // 微量提前量，防 start 与图形调度同刻的启动毛刺
    for (const buf of [this.music, this.vocal]) {
      const s = this.ctx.createBufferSource();
      s.buffer = buf;
      if (buf === this.vocal) s.connect(this.vocalHP);
      else s.connect(this.musicGain);
      s.start(t0, Math.min(fromAbs, buf.duration));
      this.sources.push(s);
    }
    this.startCtxTime = t0;
    this.offset = fromAbs;
    this.applyEnvelope(fromAbs, t0);
  }

  async play() {
    if (this._playing) return;
    // 先同步占位：await resume() 是真实异步边界（自动播放策略下可达几十毫秒），
    // 快速双击 play / 播后立停的 pause 都可能落进这个窗口——占位让重入调用走各自守卫，
    // 而不是像旧版那样 pause 被吞、双 play 排两个永不清理的 ticker。
    this._playing = true;
    try {
      await this.ctx.resume(); // 浏览器自动播放策略：上下文可能处于 suspended
    } catch (e) {
      this._playing = false; // resume 失败（含等待期 dispose 关闭上下文）回滚占位
      throw e;
    }
    if (!this._playing) return; // 等待期被 pause()：放弃启动，用户的暂停不被吞
    if (this.offset >= this.duration) this.offset = 0; // 播完再播=从头
    this.startSources(this.offset);
    window.clearInterval(this.ticker); // 兜底双保险：设新句柄前必清旧句柄，句柄覆盖在结构上不可能
    this.ticker = window.setInterval(() => this.onTick?.(this.time), 80);
  }

  pause() {
    if (!this._playing) return;
    this.offset = this.time; // 记账当前位置，下次 play 从这续
    this._playing = false;
    window.clearInterval(this.ticker);
    this.sources.forEach((s) => {
      try {
        s.stop();
      } catch {
        /* 同 startSources */
      }
      s.disconnect();
    });
    this.sources = [];
  }

  seek(t: number) {
    const was = this._playing;
    this.offset = Math.max(0, Math.min(this.duration, t));
    if (was) {
      this.startSources(this.offset); // 播放中：重建 source 并按新位置重铺 automation
    } else {
      // 暂停中：只让增益立刻取到新位置的包络值（起播时 startSources 还会再铺一次）
      this.applyEnvelope(this.offset, this.ctx.currentTime);
      this.onTick?.(this.offset);
    }
  }

  /** 恒值人声增益（dB）。有包络时仅记录——包络完全替代 vocal_gain（与后端二选一编译一致），清除包络后生效。 */
  setVocalGain(db: number) {
    this.vocalGainDB = db;
    if (this.env.length === 0) this.applyEnvelope(this.time, this.ctx.currentTime);
  }

  /**
   * 伴奏增益（dB）：实时预览生效（M1 终审裁决——「仅导出生效」造成预览/导出听感断裂）。
   * 静音时监听置 0 但仍记录推子值，取消静音即回来；导出侧同值随参数提交后端。
   */
  setMusicGain(db: number) {
    this.musicGainDB = db;
    this.musicGain.gain.value = this.musicMuted ? 0 : dbToLin(db);
  }

  /**
   * 人声低切（Hz，0=关）。旁路不重连节点：拓扑固定（vocal→HP→gain），关=把频率设 5Hz 近旁路——
   * 5Hz 远低于人声基频，频响近似直通，且免去 source 重连的爆音/抖动；导出侧后端才是真摘滤镜。
   */
  setVocalHighpass(hz: number) {
    this.vocalHP.frequency.value = hz > 0 ? hz : 5;
  }

  /** 轨道静音：只动监听（GainNode 置 0），不改参数语义（plan §6.3）。 */
  setMute(track: "music" | "vocal", mute: boolean) {
    if (track === "music") this.musicMuted = mute;
    else this.vocalMuted = mute;
    this.applyEnvelope(this.time, this.ctx.currentTime); // 人声静音需覆盖在铺的包络 ramp
    this.applyGains();
  }

  /** 下发包络点列（null=清除，回退恒 vocal_gain）；稳定排序后整层重铺，保持当前播放位置无缝。 */
  setEnvelope(points: EnvPoint[] | null) {
    this.env = points ? sortEnv(points) : [];
    this.applyEnvelope(this.time, this.ctx.currentTime);
  }

  dispose() {
    this.pause();
    void this.ctx.close(); // 释放两轨 AudioBuffer（4 分钟歌约 170MB 量级）与上下文
  }
}
