import React, { lazy } from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter, Routes, Route, Navigate, useSearchParams } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import Layout from "./components/Layout";
import RequireAuth from "./components/RequireAuth";
import { ToastProvider } from "./ui";

// 页面路由级代码分割：每页独立 chunk 按需加载（Layout 壳与全局播放器保持常驻），
// 加载期由 Layout 内的 Suspense 骨架占位。重页面（妙记/历史/字幕）不再进主包。
const LandingPage = lazy(() => import("./pages/LandingPage"));
const LoginPage = lazy(() => import("./pages/LoginPage"));
const WorkbenchPage = lazy(() => import("./pages/WorkbenchPage"));
const TTSPage = lazy(() => import("./pages/TTSPage"));
const PricingPage = lazy(() => import("./pages/PricingPage"));
const ASRPage = lazy(() => import("./pages/ASRPage"));
const PodcastPage = lazy(() => import("./pages/PodcastPage"));
const SeparatePage = lazy(() => import("./pages/SeparatePage"));
const PostPage = lazy(() => import("./pages/post/PostPage"));
const AudioEditorPage = lazy(() => import("./pages/AudioEditorPage"));
const GsgcPage = lazy(() => import("./pages/GsgcPage"));
const TranslatePage = lazy(() => import("./pages/TranslatePage"));
const MinutesPage = lazy(() => import("./pages/MinutesPage"));
const SubtitlesPage = lazy(() => import("./pages/SubtitlesPage"));
const AboutPage = lazy(() => import("./pages/AboutPage"));
const HistoryPage = lazy(() => import("./pages/HistoryPage"));
const SettingsPage = lazy(() => import("./pages/SettingsPage"));
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

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <BrowserRouter>
          <Routes>
            {/* 公开路由：产品展示页与登录页（无侧栏壳） */}
            <Route path="/" element={<LandingPage />} />
            <Route path="/login" element={<LoginPage />} />
            {/* 应用路由：登录门 + 首启强制改密 */}
            <Route
              element={
                <RequireAuth>
                  <Layout />
                </RequireAuth>
              }
            >
              <Route path="/workbench" element={<WorkbenchPage />} />
              <Route path="/tts" element={<TTSPage />} />
              <Route path="/tts-long" element={<Navigate to="/tts?tab=long" replace />} />
              <Route path="/tts-stream" element={<Navigate to="/tts?tab=stream" replace />} />
              <Route path="/pricing" element={<PricingPage />} />
              <Route path="/asr" element={<ASRPage />} />
              <Route path="/podcast" element={<PodcastPage />} />
              <Route path="/separate" element={<SeparatePage />} />
              <Route path="/post" element={<PostPage />} />
              {/* 旧三页路由并入 /post 的 Tab：查询参数整体搬迁，深链（?task=/?artifact=/?vocal=）不失效 */}
              <Route path="/mixer" element={<LegacyPostRedirect tab="mixer" />} />
              <Route path="/clip" element={<LegacyPostRedirect tab="clip" />} />
              <Route path="/duck" element={<LegacyPostRedirect tab="duck" />} />
              <Route path="/audio-edit" element={<AudioEditorPage />} />
              <Route path="/gsgc" element={<GsgcPage />} />
              <Route path="/translate" element={<TranslatePage />} />
              <Route path="/minutes" element={<MinutesPage />} />
              <Route path="/subtitles" element={<SubtitlesPage />} />
              <Route path="/about" element={<AboutPage />} />
              <Route path="/history" element={<HistoryPage />} />
              <Route path="/settings" element={<SettingsPage />} />
            </Route>
          </Routes>
        </BrowserRouter>
      </ToastProvider>
    </QueryClientProvider>
  </React.StrictMode>
);
