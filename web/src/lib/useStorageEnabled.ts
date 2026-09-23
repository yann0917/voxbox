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

/** 凭证卡字段（secret 只回传 has_value）。 */
export interface ProviderFieldShape {
  key: string;
  label: string;
  kind: "text" | "secret" | "select";
  value?: string;
  has_value?: boolean;
  options?: { value: string; label: string }[];
  placeholder?: string;
  hint?: string;
  required?: boolean;
}

/** 凭证卡/能力卡（云端=凭证卡，本地=只读能力展示）。 */
export interface ProviderShape {
  name: string;
  title: string;
  description: string;
  kind: "cloud" | "local";
  order: number;
  configured: boolean;
  tools_count: number;
  fields: ProviderFieldShape[];
}

export interface SettingsShape {
  providers: ProviderShape[];
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
  data_dir: string;
}

/**
 * 设置读取（与设置页共用 ["settings"] 缓存：设置页保存后自动刷新）。
 */
export function useSettings() {
  return useQuery({
    queryKey: ["settings"],
    queryFn: () => fetchJSON<SettingsShape>("/api/settings"),
  });
}

/**
 * 对象存储配置与启用态：enabled = provider 已配置且连接参数齐全，
 * URL-only 工具页据此展示「本地上传」通道。
 */
export function useStorageEnabled() {
  const q = useSettings();
  return {
    storage: q.data?.storage,
    enabled: !!q.data?.storage?.enabled,
    isLoading: q.isLoading,
  };
}

/** 全部卡（云端+本地）。 */
export function useProviders() {
  const q = useSettings();
  return { providers: q.data?.providers ?? [], isLoading: q.isLoading, refetch: q.refetch };
}

/** 指定厂商是否已配置凭证（undefined=设置未加载完成）。 */
export function useProviderConfigured(name: string) {
  const q = useSettings();
  return q.data?.providers.find((p) => p.name === name)?.configured;
}
