/**
 * 前端直出文件下载：Blob + 临时 <a download> 触发保存（无需服务端往返）。
 * 与 ics.ts 的 downloadICS 同机制，抽通用形态供纪要导出等复用。
 */

/** 把文本内容以指定 MIME 触发浏览器下载。 */
export function downloadBlob(content: string, filename: string, mime = "text/plain;charset=utf-8"): void {
  const url = URL.createObjectURL(new Blob([content], { type: mime }));
  try {
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    a.click();
  } finally {
    URL.revokeObjectURL(url);
  }
}
