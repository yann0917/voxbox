import { useQuery } from "@tanstack/react-query";
import { BookOpen } from "lucide-react";
import { fetchJSON } from "../lib/api";
import { Select } from "../ui";

interface DictItem {
  name: string;
  hotwords: string;
  terms: string;
}

/** 从命名词典一键填入热词/术语（词典管理走 CLI：voxbox dict add/rm）。 */
export function DictFill({ field, onFill }: { field: "hotwords" | "terms"; onFill: (value: string) => void }) {
  const { data } = useQuery({
    queryKey: ["dicts"],
    queryFn: () => fetchJSON<DictItem[]>("/api/dicts"),
    staleTime: 60_000,
  });
  const options = (data ?? []).filter((d) => (field === "hotwords" ? d.hotwords : d.terms));
  if (options.length === 0) return null;
  const label = field === "hotwords" ? "热词" : "术语";
  return (
    <div className="flex items-center gap-2">
      <span className="flex shrink-0 items-center gap-1 text-[11px] text-muted">
        <BookOpen size={11} strokeWidth={1.75} />
        词典
      </span>
      <Select
        value=""
        aria-label={`从词典填入${label}`}
        onChange={(e) => {
          const d = options.find((o) => o.name === e.target.value);
          if (d) onFill(field === "hotwords" ? d.hotwords : d.terms);
        }}
        className="min-w-0 flex-1"
      >
        <option value="">选择词典填入{label}…</option>
        {options.map((o) => {
          const count = (field === "hotwords" ? o.hotwords : o.terms).split(/[,，\n]/).filter(Boolean).length;
          return (
            <option key={o.name} value={o.name}>
              {o.name} · {count} 条
            </option>
          );
        })}
      </Select>
    </div>
  );
}
