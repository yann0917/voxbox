import { type FormEvent, useState } from "react";
import { Link, Navigate, useSearchParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { AudioLines, ShieldAlert } from "lucide-react";
import { login, useMe } from "../lib/auth";
import { Button, Input, MicroLabel } from "../ui";

/** 登录页（公开路由）：登录成功写回身份缓存并跳回 next；已登录直接进控制台。 */
export default function LoginPage() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [params] = useSearchParams();
  const qc = useQueryClient();

  const next = params.get("next");
  const nextTarget = next && next.startsWith("/") ? next : "/workbench";
  // 已登录判定必须校验服务端会话（useMe 发 /api/auth/me）：react-query 内存缓存
  // 在新开的页面加载里是空的，只查缓存会让持有效 cookie 的用户停在登录表单。
  const { data: me, isPending } = useMe();
  if (isPending) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <p className="text-sm text-muted" role="status">正在校验登录态…</p>
      </div>
    );
  }
  if (me) return <Navigate to={nextTarget} replace />;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    if (!username.trim() || !password) {
      setErr("请输入用户名与密码");
      return;
    }
    setBusy(true);
    try {
      const user = await login(username.trim(), password);
      // 写回身份缓存触发上方已登录分支重渲染，由 <Navigate> 统一跳 next
      qc.setQueryData(["me"], user);
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "登录失败");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center p-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <div className="flex size-12 items-center justify-center rounded-[var(--radius-md)] bg-accent text-accent-ink shadow-[var(--shadow-2)]">
            <AudioLines size={24} strokeWidth={2} />
          </div>
          <div>
            <h1 className="text-lg font-semibold tracking-tight text-fg">voxbox</h1>
            <p className="mt-1 text-xs text-muted">多引擎语音工作台 · 自托管部署</p>
          </div>
        </div>

        <form
          onSubmit={submit}
          className="space-y-4 rounded-[var(--radius-lg)] border border-line bg-panel p-6 shadow-[var(--shadow-2)]"
        >
          <div className="space-y-1.5">
            <MicroLabel>用户名</MicroLabel>
            <Input
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoFocus
              autoComplete="username"
              placeholder="admin"
            />
          </div>
          <div className="space-y-1.5">
            <MicroLabel>密码</MicroLabel>
            <Input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
            />
          </div>
          {err && (
            <p className="flex items-start gap-1.5 text-xs text-danger" role="alert">
              <ShieldAlert size={14} className="mt-px shrink-0" strokeWidth={1.75} />
              {err}
            </p>
          )}
          <Button type="submit" className="w-full" loading={busy}>
            登录
          </Button>
        </form>

        <p className="mt-6 text-center text-[11px] text-muted">
          <Link to="/" className="transition-colors hover:text-fg-2">
            ← 返回产品介绍
          </Link>
        </p>
      </div>
    </div>
  );
}
