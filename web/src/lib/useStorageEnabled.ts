import { useQuery } from "@tanstack/react-query";
import { fetchJSON } from "./api";

/** 单个存储类型的独立配置段（secret 只回传 has_secret_key）。 */
export interface StorageChannelShape {
  endpoint: string;
  region: string;
  bucket: string;
  access_key: string;
  has_secret_key: boolean;
  prefix: string;
}

export interface SettingsShape {
  volc: {
    speech: { app_id: string; has_access_token: boolean; api_key: string };
    mediakit: { has_api_key: boolean };
  };
  storage?: {
    provider: string;
    endpoint: string;
    region: string;
    bucket: string;
    access_key: string;
    has_secret_key: boolean;
    prefix: string;
    enabled: boolean;
    /** 各存储类型独立配置（键=provider 名）：切换类型按段换显，互不覆盖。 */
    channels: Record<string, StorageChannelShape>;
  };
  mvsep?: { has_api_token: boolean; base_url: string };
  data_dir: string;
}

/**
 * 对象存储配置与启用态（与设置页共用 ["settings"] 缓存：设置页保存后此 hook 自动刷新）。
 * enabled = provider 已配置且连接参数齐全，URL-only 工具页据此展示「本地上传」通道。
 */
export function useStorageEnabled() {
  const q = useQuery({
    queryKey: ["settings"],
    queryFn: () => fetchJSON<SettingsShape>("/api/settings"),
  });
  return {
    storage: q.data?.storage,
    enabled: !!q.data?.storage?.enabled,
    isLoading: q.isLoading,
  };
}
