import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { changePassword, login, useMe } from "../lib/auth";
import { Button, Input, MicroLabel } from "../ui";
import { AudioLines, KeyRound, ShieldAlert } from "lucide-react";

/** 鉴权路由守卫：未登录跳 /login（带回跳地址）；首启引导密码强制改密后才放行使用。 */
export default function RequireAuth({ children }: { children: ReactNode }) {
  const { data, isPending, isError } = useMe();
  const location = useLocation();

  if (isPending) {
    return (
      <div className="flex h-screen items-center justify-center bg-bg">
        <div className="flex items-center gap-2 text-sm text-muted" role="status">
          <AudioLines size={16} className="text-accent" />
          正在校验登录态…
        </div>
      </div>
    );
  }
  if (isError || !data) {
    const next = encodeURIComponent(location.pathname + location.search);
    return <Navigate to={`/login?next=${next}`} replace />;
  }
  return (
    <>
      {children}
      {data.must_change_password && <ForcedChangePassword />}
    </>
  );
}

/** 首启随机密码的强制改密弹层：不可关闭，改完自动用新密码重登并刷新身份。 */
function ForcedChangePassword() {
  const [oldPw, setOldPw] = useState("");
  const [newPw, setNewPw] = useState("");
  const [confirm, setConfirm] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const qc = useQueryClient();
  const { data: me } = useMe();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErr("");
    if (newPw.length < 8) {
      setErr("新密码至少 8 位");
      return;
    }
    if (newPw !== confirm) {
      setErr("两次输入的新密码不一致");
      return;
    }
    setBusy(true);
    try {
      await changePassword(oldPw, newPw);
      // 服务端改密后全端下线：立即用新密码重登，保持本端可用
      if (me) await login(me.username, newPw);
      await qc.invalidateQueries({ queryKey: ["me"] });
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "修改失败");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm">
      <form
        onSubmit={submit}
        className="w-full max-w-md space-y-5 rounded-[var(--radius-lg)] border border-line bg-panel p-6 shadow-[var(--shadow-3)]"
      >
        <div className="flex items-center gap-3">
          <div className="flex size-9 items-center justify-center rounded-[var(--radius-sm)] bg-accent text-accent-ink">
            <KeyRound size={17} strokeWidth={2} />
          </div>
          <div>
            <p className="text-sm font-semibold text-fg">请修改初始密码</p>
            <p className="text-xs text-muted">首次登录使用的是一次性引导密码，修改后才能继续使用</p>
          </div>
        </div>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <MicroLabel>当前密码</MicroLabel>
            <Input
              type="password"
              value={oldPw}
              onChange={(e) => setOldPw(e.target.value)}
              autoFocus
              autoComplete="current-password"
            />
          </div>
          <div className="space-y-1.5">
            <MicroLabel>新密码（至少 8 位）</MicroLabel>
            <Input
              type="password"
              value={newPw}
              onChange={(e) => setNewPw(e.target.value)}
              autoComplete="new-password"
            />
          </div>
          <div className="space-y-1.5">
            <MicroLabel>确认新密码</MicroLabel>
            <Input
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              autoComplete="new-password"
            />
          </div>
        </div>
        {err && (
          <p className="flex items-start gap-1.5 text-xs text-danger" role="alert">
            <ShieldAlert size={14} className="mt-px shrink-0" strokeWidth={1.75} />
            {err}
          </p>
        )}
        <Button type="submit" className="w-full" loading={busy}>
          修改密码并继续
        </Button>
      </form>
    </div>
  );
}
