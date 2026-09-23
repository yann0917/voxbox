// 选区试听引擎：单 AudioBuffer 播放 [from, to) 选区，是后端 audio/clip 导出链的前端孪生
// （internal/provider/audiotool/clip.go 的 atrim=start:end ≙ start(0, from, to-from)）。
// MixerEngine（lib/mixer.ts）的极简表亲：单缓冲、零处理链、无 automation；
// P0 交接修正点：onended 在自然播完时也落 playing=false（MixerEngine 缺失的正确性在此做对）。

/**
 * 选区播放器：AudioBufferSourceNode 是一次性节点且不可暂停，play(from,to) 恒
 * 「停旧建新」（stop/rebuild 语义同 MixerEngine.startSources）。终态两类，onended
 * 都会触发但记账不同——自然播完：playing=false + 驻留 to + onEnded 回调；
 * stop()/重播顶替：以「现役 source 身份」判别后忽略，驻留点归 stop 记账。
 */
export class RegionPlayer {
  private ctx: AudioContext;
  private buffer: AudioBuffer;
  private source: AudioBufferSourceNode | null = null;
  /** 本次启动锚点：播放中 time = from + (ctx.currentTime - startCtxTime)，钳 [from, to]。 */
  private startCtxTime = 0;
  private from = 0;
  private to = 0;
  private _playing = false;
  /** 非播放态驻留位置：stop 记账停点、自然播完落 to（get time 停止态读这里）。 */
  private parkedAt = 0;
  private ticker?: number;
  /** 播放中的选区位置回调（约 80ms 一次，UI 时间码/播放头订阅用；同 MixerEngine.onTick）。 */
  onTick?: (t: number) => void;
  /** 选区自然播完回调（stop() 不触发）——页面据此落播放态，与引擎态保持一致。 */
  onEnded?: () => void;

  // 私有构造：AudioContext 在 load() 里创建后传入——解码失败时上下文随 load 一并回收，
  // 不存在「建了没解码成功」的泄漏实例（同 MixerEngine）。
  private constructor(ctx: AudioContext, buffer: AudioBuffer) {
    this.ctx = ctx;
    this.buffer = buffer;
  }

  /** 拉取并解码音频（artifact stream / uploads stream，cookie 会话自动携带）。 */
  static async load(url: string): Promise<RegionPlayer> {
    const ctx = new AudioContext();
    try {
      const r = await fetch(url);
      if (!r.ok) throw new Error(`音频加载失败 (${r.status})`);
      const buffer = await ctx.decodeAudioData(await r.arrayBuffer());
      return new RegionPlayer(ctx, buffer);
    } catch (e) {
      void ctx.close(); // 任一拉取/解码失败都回收上下文，别占着硬件资源
      throw e;
    }
  }

  get duration() {
    return this.buffer.duration;
  }

  get playing() {
    return this._playing;
  }

  /** 当前播放位置。source 未调度（play 的 await resume() 启动窗口）时停在待播起点——
   *  此刻 startCtxTime 还是旧锚点，elapsed 项是陈值不能外露（重入窗口的 time 读取安全）。 */
  get time() {
    if (!this._playing) return this.parkedAt;
    if (!this.source) return this.from;
    return Math.min(this.to, this.from + Math.max(0, this.ctx.currentTime - this.startCtxTime));
  }

  /**
   * 播放选区 [from, to)：每次调用都停旧 source 从 from 重建（选中即重播起点）。
   * resume() 可能 reject（自动播放策略/等待期 dispose 关闭上下文）——原样抛给
   * 调用方 .catch 提示，内部回滚占位态（同 MixerEngine.play）。
   */
  async play(from: number, to: number) {
    this.stopSource(); // 停旧建新：旧 source 的 onended 稍后触发时因身份不符被忽略
    this.from = Math.max(0, Math.min(from, this.duration));
    this.to = Math.max(this.from, Math.min(to, this.duration));
    this.parkedAt = this.from;
    // 先同步占位：await resume() 是真实异步边界（自动播放策略下可达几十毫秒），
    // 快速重入的 play/stop 都可能落进这个窗口——占位让重入调用走各自守卫。
    this._playing = true;
    try {
      await this.ctx.resume(); // 浏览器自动播放策略：上下文可能处于 suspended
    } catch (e) {
      this._playing = false; // resume 失败回滚占位
      throw e;
    }
    if (!this._playing) return; // 等待期被 stop()：放弃启动，用户的停止不被吞
    const t0 = this.ctx.currentTime + 0.02; // 微量提前量，防 start 与图形调度同刻的启动毛刺
    const s = this.ctx.createBufferSource();
    s.buffer = this.buffer;
    s.connect(this.ctx.destination);
    // onended 在自然播完与 stop()/顶替时都会触发：仅「现役且占位有效」才算自然播完——
    // 落 playing=false + 驻留 to（P0 MixerEngine 忘落播放态的遗留项在此修正）。
    s.onended = () => {
      if (this.source !== s || !this._playing) return;
      this._playing = false;
      this.source = null;
      this.parkedAt = this.to;
      window.clearInterval(this.ticker);
      this.onTick?.(this.parkedAt);
      this.onEnded?.();
    };
    s.start(t0, this.from, Math.max(0.001, this.to - this.from));
    this.source = s;
    this.startCtxTime = t0;
    window.clearInterval(this.ticker); // 兜底双保险：设新句柄前必清旧句柄
    this.ticker = window.setInterval(() => this.onTick?.(this.time), 80);
  }

  stop() {
    if (this._playing) {
      this.parkedAt = this.time; // 记账当前位置（同 MixerEngine.pause 的 offset 记账）
      this._playing = false; // 先落态：stop 触发的 onended 走「非现役/占位失效」忽略分支
    }
    this.stopSource(); // 非播放态也兜底清残留 source（resume 失败窗口等）
    window.clearInterval(this.ticker);
    this.onTick?.(this.parkedAt);
  }

  /** 停掉并拆掉现役 source（若有）。不动画状态与驻留点——记账归调用方。 */
  private stopSource() {
    const s = this.source;
    this.source = null;
    if (!s) return;
    try {
      s.stop();
    } catch {
      /* 已自然播完的 source 再 stop 会抛，忽略（同 MixerEngine） */
    }
    s.disconnect();
  }

  dispose() {
    this.stop();
    void this.ctx.close(); // 释放 AudioBuffer（4 分钟歌约 85MB 量级）与上下文
  }
}
