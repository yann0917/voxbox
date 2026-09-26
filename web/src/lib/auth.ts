// 登录态与凭证管理：/api/auth/* 的薄封装。会话走 HttpOnly Cookie（浏览器自动携带），
// 前端只持身份快照（useMe）；401 由 RequireAuth 统一跳登录页。
import { useQuery } from "@tanstack/react-query";
import { fetchJSON } from "./api";

export interface Me {
  id: string;
  username: string;
  role: string;
  must_change_password: boolean;
  has_token: boolean;
  // 桌面形态标记：VOXBOX_DESKTOP=1 时后端返回 true；浏览器下恒缺省（undefined），
  // 前端据此做形态分流（公开路由直达工作台、退出项与 Token 卡隐藏）。
  desktop?: boolean;
}

export const fetchMe = () => fetchJSON<Me>("/api/auth/me");

export const login = (username: string, password: string) =>
  fetchJSON<Me>("/api/auth/login", { method: "POST", body: JSON.stringify({ username, password }) });

export const logout = () => fetchJSON<{ ok: boolean }>("/api/auth/logout", { method: "POST" });

export const changePassword = (old_password: string, new_password: string) =>
  fetchJSON<{ ok: boolean }>("/api/auth/password", {
    method: "POST",
    body: JSON.stringify({ old_password, new_password }),
  });

/** 轮换 API token：明文只在本响应出现一次（MCP/CLI 用）。 */
export const rotateToken = () =>
  fetchJSON<{ api_token: string }>("/api/auth/token/rotate", { method: "POST" });

export function useMe() {
  return useQuery({ queryKey: ["me"], queryFn: fetchMe, retry: false, staleTime: 60_000 });
}
