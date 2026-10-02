/**
 * 实时字幕会话客户端（/api/ws/live）：一条连接一次会话——开始 → 音频分片 → 停止 →
 * 定格 →（可多次保存）。同源相对连接（dev 走 vite 代理，https 部署自动 wss），
 * 鉴权走登录 Cookie（浏览器自动携带）。
 *
 * 协议纪律（与服务端契约逐字对齐）：
 *  - 消息一律按 type 分派，不假设帧间顺序：ready 与首个增量、停止与定格之间的
 *    残余增量都可能交错，消费方自行容错；
 *  - 服务端只发文本 JSON 帧；错误帧后连接是否保留随场景（引擎差异/推送失败），
 *    由上层按状态机处理，本类只忠实转交；
 *  - 音频分片在 start 之后发送即可（WS 保序，无需等 ready 应答）。
 */

import type { LiveFinalPayload, LivePartialPayload, LiveSegment } from "./liveCaption";

export type LiveEngine = "volcengine" | "local";

export type LiveServerMsg =
  | { type: "ready"; engine: string }
  | ({ type: "partial" } & LivePartialPayload)
  | ({ type: "final" } & LiveFinalPayload)
  | { type: "saved"; task_id: string }
  | { type: "error"; message: string };

export interface LiveStartOptions {
  engine: LiveEngine;
  language?: string;
  hotwords?: string;
  /** 说话人分离（仅火山引擎生效） */
  speaker?: boolean;
}

export class LiveClient {
  private ws: WebSocket | null = null;
  private opened = false;

  onMessage: (m: LiveServerMsg) => void = () => {};
  onClose: () => void = () => {};

  static url(): string {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    return `${proto}//${location.host}/api/ws/live`;
  }

  get connected(): boolean {
    return this.ws !== null && this.opened;
  }

  /** 建立连接；resolve 于 open，失败（拒绝/未开先断）reject。 */
  connect(): Promise<void> {
    return new Promise((resolve, reject) => {
      let settled = false;
      const ws = new WebSocket(LiveClient.url());
      ws.binaryType = "arraybuffer";
      this.ws = ws;
      ws.onopen = () => {
        this.opened = true;
        if (!settled) {
          settled = true;
          resolve();
        }
      };
      ws.onerror = () => {
        // 错误事件无细节：未开先错在此 reject，open 后的错误由 onclose 收口
        if (!settled) {
          settled = true;
          reject(new Error("无法建立实时字幕通道"));
        }
      };
      ws.onclose = () => {
        this.ws = null;
        this.opened = false;
        if (!settled) {
          settled = true;
          reject(new Error("实时字幕通道连接失败"));
        }
        this.onClose();
      };
      ws.onmessage = (e) => {
        if (typeof e.data !== "string") return; // 服务端只发文本帧，防御二进制脏帧
        let m: LiveServerMsg;
        try {
          m = JSON.parse(e.data) as LiveServerMsg;
        } catch {
          return; // 忽略坏帧
        }
        this.onMessage(m);
      };
    });
  }

  /** 开始会话。文本帧先行入队，随后的音频分片经同一条连接保序到达。 */
  sendStart(opts: LiveStartOptions): void {
    this.sendJSON({ type: "start", ...opts });
  }

  /** 上送音频分片（16kHz s16le 单声道原始字节）。 */
  sendAudio(chunk: Uint8Array<ArrayBuffer>): void {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(chunk);
  }

  /** 结束音频输入，服务端收束后回定格全量。 */
  sendStop(): void {
    this.sendJSON({ type: "stop" });
  }

  /** 存为识别任务（定格后可用，可多次调用各生成新任务）。 */
  sendSave(): void {
    this.sendJSON({ type: "save" });
  }

  /** 主动断开（幂等）：不再触发 onClose 回调。 */
  close(): void {
    const ws = this.ws;
    if (!ws) return;
    this.ws = null;
    this.opened = false;
    ws.onopen = ws.onmessage = ws.onclose = ws.onerror = null;
    ws.close();
  }

  private sendJSON(v: unknown): void {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(v));
  }
}

export type { LiveSegment };
