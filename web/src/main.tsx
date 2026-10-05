import React, { lazy } from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter, Routes, Route, Navigate, useSearchParams } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import Layout from "./components/Layout";
import RequireAuth from "./components/RequireAuth";
import { useMe } from "./lib/auth";
import { features } from "./features";
import { ToastProvider } from "./ui";

// 公开页与旧路由重定向保持显式；应用内功能路由自功能清单（features.ts）派生——
// 新增功能页面不改本文件。lazy 在模块级建一次，render 期不重建组件树。
const LandingPage = lazy(() => import("./pages/LandingPage"));
const LoginPage = lazy(() => import("./pages/LoginPage"));
const featureRoutes = features.map((f) => ({ id: f.id, path: f.path, Page: lazy(f.component) }));

// 自托管字体（离线可用，不依赖 Google CDN）；仅 latin 子集，中文走系统回退
import "@fontsource/fira-sans/latin-400.css";
import "@fontsource/fira-sans/latin-500.css";
import "@fontsource/fira-sans/latin-600.css";
import "@fontsource/fira-code/latin-400.css";
import "@fontsource/fira-code/latin-500.css";
import "./theme.css";

const qc = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: false, staleTime: 10_000 } },
});

// 旧单页路由（/mixer /clip /duck）→ /post?tab=<原页>：当前查询参数原样随迁
function LegacyPostRedirect({ tab }: { tab: "mixer" | "clip" | "duck" }) {
  const [params] = useSearchParams();
  const next = new URLSearchParams(params);
  next.set("tab", tab);
  return <Navigate to={`/post?${next.toString()}`} replace />;
}

// 桌面形态公开路由门：desktop=true 时 Landing/登录页不可达，一律直达工作台。
// 非桌面（浏览器）下 desktop 恒为 undefined，公开路由行为不变。
function DesktopGate({ children }: { children: React.ReactNode }) {
  const { data } = useMe();
  if (data?.desktop) return <Navigate to="/workbench" replace />;
  return <>{children}</>;
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <BrowserRouter>
          <Routes>
            {/* 公开路由：产品展示页与登录页（无侧栏壳）；桌面形态经 DesktopGate 直达工作台 */}
            <Route path="/" element={<DesktopGate><LandingPage /></DesktopGate>} />
            <Route path="/login" element={<DesktopGate><LoginPage /></DesktopGate>} />
            {/* 应用路由：登录门 + 首启强制改密 */}
              <Route
                element={
                  <RequireAuth>
                    <Layout />
                  </RequireAuth>
                }
              >
                {/* 应用功能路由:自功能清单派生(路径/懒加载组件都在 features.ts) */}
                {featureRoutes.map(({ id, path, Page }) => (
                  <Route key={id} path={path} element={<Page />} />
                ))}
                {/* 旧三页路由并入 /post 的 Tab：查询参数整体搬迁，深链（?task=/?artifact=/?vocal=）不失效 */}
                <Route path="/mixer" element={<LegacyPostRedirect tab="mixer" />} />
                <Route path="/clip" element={<LegacyPostRedirect tab="clip" />} />
                <Route path="/duck" element={<LegacyPostRedirect tab="duck" />} />
                {/* 旧 TTS 子路由并入 /tts 的 Tab */}
                <Route path="/tts-long" element={<Navigate to="/tts?tab=long" replace />} />
                <Route path="/tts-stream" element={<Navigate to="/tts?tab=stream" replace />} />
              </Route>
          </Routes>
        </BrowserRouter>
      </ToastProvider>
    </QueryClientProvider>
  </React.StrictMode>
);
