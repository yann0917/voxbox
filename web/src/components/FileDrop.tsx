import { useRef, useState, type DragEvent } from "react";
import { AlertTriangle, Upload, X } from "lucide-react";
import { IconButton } from "../ui";

interface FileDropProps {
  /** 单文件模式的受控值（与 multiple 互斥使用） */
  file?: File | null;
  onFile?: (f: File | null) => void;
  /** 多文件模式（工作台批量识别）：受控 File 数组，拖入/选择为追加语义由父层决定 */
  multiple?: boolean;
  files?: File[];
  onFiles?: (fs: File[]) => void;
  /** 文件选择器的 accept 值（如 ".mp3,.wav"，须与页面文案/后端白名单一致） */
  accept: string;
  /** 空态提示（支持的格式说明） */
  emptyHint: string;
  /** 选择区域标签（无障碍） */
  label: string;
  /** 校验失败等错误信息 */
  error?: string;
}

/** 本地文件拖放/点选区：人声分离/妙记的单文件与工作台批量识别的多文件上传通道
 *（文件经服务端中转对象存储）。 */
export function FileDrop({ file, onFile, multiple, files, onFiles, accept, emptyHint, label, error }: FileDropProps) {
  const [dragging, setDragging] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const emit = (picked: File[]) => {
    if (multiple) {
      onFiles?.(picked);
    } else {
      onFile?.(picked[0] ?? null);
    }
  };

  const onDrop = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setDragging(false);
    const picked = Array.from(e.dataTransfer.files ?? []);
    if (picked.length === 0) return;
    emit(multiple ? [...(files ?? []), ...picked] : [picked[0]]);
  };

  const showFiles = multiple ? (files ?? []) : file ? [file] : [];

  return (
    <div className="space-y-2">
      <div
        role="button"
        tabIndex={0}
        aria-label={label}
        onClick={() => inputRef.current?.click()}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            inputRef.current?.click();
          }
        }}
        onDragOver={(e) => {
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={onDrop}
        className={`flex cursor-pointer flex-col items-center justify-center gap-2 rounded-[var(--radius-md)] border border-dashed px-4 py-7 text-center transition-colors duration-150 ${
          dragging ? "border-accent bg-raise-2" : "border-line-strong bg-raise-2/40 hover:border-accent"
        }`}
      >
        <span className={`flex size-9 items-center justify-center rounded-full border border-line bg-raise ${dragging ? "text-accent" : "text-muted"}`}>
          <Upload size={16} strokeWidth={1.75} />
        </span>
        {showFiles.length > 0 ? (
          multiple ? (
            <>
              <p className="text-sm text-fg">
                已选 <span className="font-mono tabular-nums">{showFiles.length}</span> 个文件
              </p>
              <p className="max-w-full truncate font-mono text-[11px] tabular-nums text-muted">
                {formatBytes(showFiles.reduce((a, f) => a + f.size, 0))}
                {" · "}
                {showFiles
                  .slice(0, 3)
                  .map((f) => f.name)
                  .join("、")}
                {showFiles.length > 3 ? " 等" : ""}
              </p>
            </>
          ) : (
            <>
              <p className="max-w-full truncate text-sm text-fg">{file!.name}</p>
              <p className="font-mono text-[11px] tabular-nums text-muted">{formatBytes(file!.size)}</p>
            </>
          )
        ) : (
          <>
            <p className="text-sm text-fg-2">拖拽文件到此处，或点击选择文件</p>
            <p className="text-[11px] text-muted">{emptyHint}</p>
          </>
        )}
        <input
          ref={inputRef}
          type="file"
          accept={accept}
          multiple={multiple}
          aria-label={label}
          className="hidden"
          onChange={(e) => {
            const picked = Array.from(e.target.files ?? []);
            if (picked.length > 0) emit(multiple ? picked : [picked[0]]);
            e.target.value = "";
          }}
        />
      </div>
      {showFiles.length > 0 && (
        <div className="flex items-center gap-2">
          <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted">
            {multiple
              ? showFiles.map((f) => f.name).join("、")
              : file!.name}
          </span>
          <IconButton
            label="清除已选文件"
            size="sm"
            onClick={(e) => {
              e.stopPropagation();
              emit([]);
            }}
          >
            <X size={14} strokeWidth={1.75} />
          </IconButton>
        </div>
      )}
      {error && (
        <p className="flex items-start gap-1.5 text-[11px] text-danger">
          <AlertTriangle size={12} strokeWidth={1.75} className="mt-0.5 shrink-0" />
          {error}
        </p>
      )}
    </div>
  );
}

function formatBytes(bytes: number): string {
  if (!bytes) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}
