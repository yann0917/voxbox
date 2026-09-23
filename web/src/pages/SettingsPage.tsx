import { useState, type ChangeEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  AudioLines,
  Bell,
  CheckCircle2,
  CloudUpload,
  Eye,
  EyeOff,
  FolderOpen,
  KeyRound,
  PlugZap,
  Server,
  XCircle,
} from "lucide-react";
import { fetchJSON } from "../lib/api";
import { rotateToken, useMe } from "../lib/auth";
import { useSettings, type ProviderShape, type ProviderFieldShape } from "../lib/useStorageEnabled";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  Field,
  IconButton,
  Input,
  MicroLabel,
  PageHeader,
  Select,
  Skeleton,
  Tabs,
  useToast,
  type TabItem,
} from "../ui";

/** test-connection 响应：results 逐卡回 name/ok/message，storage 独立段。 */
interface ConnResult {
  results: { name: string; ok: boolean; message: string }[];
  storage: { ok: boolean; message: string };
}

/** 卡名 → 中文展示名（连通性结果前缀）。 */
const CARD_NAMES: Record<string, string> = {
  volcengine: "火山语音",
  mediakit: "MediaKit",
  mvsep: "MVSep",
  qianwen: "千问",
};

/** 单个存储类型的表单草稿（未保存输入）：全字符串便于受控；secret 只收集不回显。 */
interface ChanDraft {
  endpoint?: string;
  region?: string;
  bucket?: string;
  access_key?: string;
  secret_key?: string;
  prefix?: string;
}

/** 敏感输入：默认隐藏 + 显示切换 */
function SecretInput({
  name,
  placeholder,
  id,
  ...rest
}: { name: string; placeholder: string; id?: string } & Record<string, unknown>) {
  const [show, setShow] = useState(false);
  return (
    <div className="relative">
      <Input
        id={id}
        name={name}
        type={show ? "text" : "password"}
        placeholder={placeholder}
        autoComplete="off"
        className="pr-10"
        {...rest}
      />
      <div className="absolute right-1 top-1/2 -translate-y-1/2">
        <IconButton
          label={show ? "隐藏内容" : "显示内容"}
          size="sm"
          type="button"
          onClick={() => setShow((v) => !v)}
        >
          {show ? <EyeOff size={14} strokeWidth={1.75} /> : <Eye size={14} strokeWidth={1.75} />}
        </IconButton>
      </div>
    </div>
  );
}

function ConnBadge({ result }: { result?: { ok: boolean; message: string } }) {
  if (!result) return null;
  const Icon = result.ok ? CheckCircle2 : XCircle;
  return (
    <span
      className={`inline-flex items-center gap-1.5 text-xs ${result.ok ? "text-meter" : "text-danger"}`}
      role="status"
    >
      <Icon size={14} strokeWidth={1.75} />
      <span className="break-all">{result.message}</span>
    </span>
  );
}

/** 单卡状态徽标文案 */
function cardAside(p: ProviderShape) {
  return (
    <span className={`micro ${p.configured ? "" : "text-warn"}`}>
      {p.configured ? "已配置" : "未配置"}
    </span>
  );
}

/** 动态凭证卡：字段类型驱动渲染，整卡独立保存。 */
function ProviderCardForm({ card, onSaved }: { card: ProviderShape; onSaved: () => void }) {
  const { toast } = useToast();
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () =>
      fetchJSON(`/api/settings/providers/${card.name}`, {
        method: "PUT",
        body: JSON.stringify({ fields: drafts }),
      }),
    onSuccess: () => {
      toast({ tone: "ok", title: "凭证已保存", description: "已即时生效，无需重启服务。" });
      setDrafts({});
      onSaved();
    },
    onError: (e: Error) => toast({ tone: "error", title: "保存失败", description: e.message }),
  });
  const fieldVal = (f: ProviderFieldShape) => drafts[f.key] ?? (f.kind === "select" ? (f.value ?? "") : "");
  return (
    <Card>
      <CardHeader title={card.title} icon={<KeyRound size={15} strokeWidth={1.75} />} aside={cardAside(card)} />
      <CardBody className="space-y-4">
        <p className="text-xs text-muted">{card.description}</p>
        {card.fields.map((f) => (
          <Field key={f.key} label={f.label} hint={f.hint ?? (f.kind === "secret" && f.has_value ? "当前已配置" : undefined)}>
            {({ id, ...rest }) =>
              f.kind === "select" ? (
                <Select id={id} value={fieldVal(f)} onChange={(e) => setDrafts((d) => ({ ...d, [f.key]: e.target.value }))} {...rest}>
                  {(f.options ?? []).map((o) => (
                    <option key={o.value} value={o.value}>{o.label}</option>
                  ))}
                </Select>
              ) : f.kind === "secret" ? (
                <SecretInput
                  id={id}
                  name={f.key}
                  value={fieldVal(f)}
                  onChange={(e: ChangeEvent<HTMLInputElement>) => setDrafts((d) => ({ ...d, [f.key]: e.target.value }))}
                  placeholder={f.placeholder ?? "留空表示不修改"}
                  {...rest}
                />
              ) : (
                <Input
                  id={id}
                  value={fieldVal(f)}
                  onChange={(e) => setDrafts((d) => ({ ...d, [f.key]: e.target.value }))}
                  placeholder={f.placeholder ?? ""}
                  {...rest}
                />
              )
            }
          </Field>
        ))}
        <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
          保存{card.title.split(" ·")[0]}凭证
        </Button>
      </CardBody>
    </Card>
  );
}

type SettingsTab = "cloud" | "local";

export default function SettingsPage() {
  const [tab, setTab] = useState<SettingsTab>("cloud");
  const { data, isLoading, refetch } = useSettings();
  const qc = useQueryClient();
  const cloud = (data?.providers ?? []).filter((p) => p.kind === "cloud");
  const local = (data?.providers ?? []).filter((p) => p.kind === "local");
  const TABS: TabItem<SettingsTab>[] = [
    { value: "cloud", label: "云端服务", icon: <CloudUpload size={13} strokeWidth={1.75} /> },
    { value: "local", label: "本地环境", icon: <Server size={13} strokeWidth={1.75} /> },
  ];
  const refresh = () => {
    void refetch();
    void qc.invalidateQueries({ queryKey: ["settings"] });
  };

  const { toast } = useToast();

  // —— 通知卡：localStorage 开关 + 浏览器授权 ——
  const [notifyOn, setNotifyOn] = useState(
    typeof localStorage !== "undefined" && localStorage.getItem("sysnotify") === "on"
  );
  const notifyPermission =
    typeof Notification !== "undefined" ? Notification.permission : "不支持";

  // —— 存储卡：类型受控 + 分通道草稿 ——
  const saveStorage = useMutation({
    mutationFn: (body: Record<string, unknown>) =>
      fetchJSON("/api/settings/storage", { method: "PUT", body: JSON.stringify(body) }),
    onSuccess: () => {
      toast({ tone: "ok", title: "存储配置已保存", description: "下一任务即使用新存储通道。" });
      setChanDrafts({});
      void refetch();
      void qc.invalidateQueries({ queryKey: ["settings"] });
    },
    onError: (e: Error) => toast({ tone: "error", title: "存储配置保存失败", description: e.message }),
  });
  // 存储类型走受控 state：自定义 Select 是 combobox div（无原生 name 注册），
  // FormData 读不到它的值——曾导致 provider 恒存为空串、其余字段正常但通道判定未启用。
  // null = 未动过（跟随已存启用类型）；"" = 未启用（仅停用，各类型已存配置原样保留）。
  const [storageProvider, setStorageProvider] = useState<string | null>(null);
  // 各存储类型的表单草稿：后端按类型独立存段（storage.channels.<名>），切换类型按段
  // 换显已存值，未保存的草稿也按类型各留各的——切走再切回不丢不重填。
  const [chanDrafts, setChanDrafts] = useState<Record<string, ChanDraft>>({});
  const storage = data?.storage;
  // 存储卡当前选中类型（"" = 未启用）。
  const selChannel = storageProvider ?? storage?.provider ?? "";
  // 单个类型的当前表单值：已存值打底 + 未保存草稿覆盖；secret 永不回显，恒空起填。
  const channelDraft = (name: string) => {
    const stored = storage?.channels?.[name];
    const d = chanDrafts[name] ?? {};
    return {
      endpoint: d.endpoint ?? stored?.endpoint ?? "",
      region: d.region ?? stored?.region ?? "",
      bucket: d.bucket ?? stored?.bucket ?? "",
      access_key: d.access_key ?? stored?.access_key ?? "",
      secret_key: d.secret_key ?? "",
      prefix: d.prefix ?? stored?.prefix ?? "",
      has_secret_key: stored?.has_secret_key ?? false,
    };
  };
  const setChannelDraft = (name: string, patch: Partial<ChanDraft>) =>
    setChanDrafts((prev) => ({ ...prev, [name]: { ...prev[name], ...patch } }));

  // —— API Token（MCP HTTP / CLI 远程鉴权）：明文仅签发响应出现一次 ——
  const { data: me } = useMe();
  const [newToken, setNewToken] = useState("");
  const rotate = useMutation({
    mutationFn: rotateToken,
    onSuccess: (t) => {
      setNewToken(t.api_token);
      toast({ tone: "ok", title: "Token 已生成", description: "完整 Token 仅显示这一次，请立即保存" });
    },
    onError: (e: Error) => toast({ tone: "error", title: "Token 生成失败", description: e.message }),
  });

  // —— 连通性测试 ——
  const test = useMutation({
    mutationFn: () => fetchJSON<ConnResult>("/api/settings/test-connection", { method: "POST" }),
    onError: (e: Error) => toast({ tone: "error", title: "连通性测试失败", description: e.message }),
  });

  return (
    <>
      <PageHeader title="设置" description="云端服务凭证与本地环境，保存即时生效" />
      <div className="mb-4">
        <Tabs items={TABS} value={tab} onChange={setTab} />
      </div>

      {tab === "cloud" ? (
        <div className="space-y-4">
          {isLoading ? (
            <Skeleton className="h-32 w-full" />
          ) : (
            cloud.map((p) => <ProviderCardForm key={p.name} card={p} onSaved={refresh} />)
          )}

          {storage && (
            <Card>
              <CardHeader
                title="对象存储 · 本地文件中转"
                icon={<CloudUpload size={15} strokeWidth={1.75} />}
                aside={
                  <span className={`micro ${storage.enabled ? "" : "text-warn"}`}>
                    {storage.enabled ? "已启用" : "未启用"}
                  </span>
                }
              />
              <CardBody className="space-y-4">
                <p className="text-xs text-muted">
                  语音识别（闲时/极速版）、人声分离、语音妙记的上游只收公网 URL；配置对象存储后，本地上传的文件会在任务执行时
                  <strong className="text-fg">自动转存并换取签名 URL</strong>
                  ，上游用完即弃。已支持火山引擎 TOS 与阿里云 OSS；腾讯 COS 等其他 S3 兼容通道规划中。
                </p>
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    if (selChannel === "") {
                      // 未启用：仅停用，各类型已存段原样保留，切回即恢复。
                      saveStorage.mutate({ provider: "" });
                      return;
                    }
                    const d = channelDraft(selChannel);
                    saveStorage.mutate({
                      provider: selChannel,
                      endpoint: d.endpoint.trim(),
                      region: d.region.trim(),
                      bucket: d.bucket.trim(),
                      access_key: d.access_key.trim(),
                      secret_key: d.secret_key,
                      prefix: d.prefix.trim(),
                    });
                  }}
                  className="space-y-4"
                >
                  <Field label="存储类型" hint="各类型配置独立保存、切换互不覆盖；未启用=停用但保留配置">
                    {({ id, ...rest }) => (
                      <Select
                        id={id}
                        value={selChannel}
                        onChange={(e) => setStorageProvider(e.target.value)}
                        {...rest}
                      >
                        <option value="">未启用</option>
                        <option value="tos">火山引擎 TOS</option>
                        <option value="oss">阿里云 OSS</option>
                      </Select>
                    )}
                  </Field>
                  {selChannel === "" ? (
                    <p className="text-xs text-muted">
                      已停用对象存储：各存储类型已保存的连接配置原样保留，重新选择类型即带回原值，无需重填。
                    </p>
                  ) : (
                    (() => {
                      const d = channelDraft(selChannel);
                      const up = (patch: Partial<ChanDraft>) => setChannelDraft(selChannel, patch);
                      // 分通道提示：endpoint/凭证体系两家不同，region 均参与签名
                      const isOSS = selChannel === "oss";
                      const endpointPh = isOSS ? "oss-cn-hangzhou.aliyuncs.com" : "tos-cn-beijing.volces.com";
                      const endpointHint = isOSS ? "如 oss-cn-hangzhou.aliyuncs.com（与桶所在地域一致）" : "如 tos-cn-beijing.volces.com（与桶所在地域一致）";
                      const regionHint = isOSS ? "如 cn-hangzhou（参与 V4 签名）" : "如 cn-beijing";
                      const akHint = isOSS ? "阿里云 RAM 的 AccessKey ID" : "火山引擎 IAM 的 AK";
                      const skHint = isOSS ? "阿里云 RAM 的 AccessKey Secret" : "火山引擎 IAM 的 SK";
                      return (
                        <div className="space-y-4">
                          <div className="grid gap-4 sm:grid-cols-2">
                            <Field label="Endpoint" hint={endpointHint}>
                              {({ id, ...rest }) => (
                                <Input id={id} value={d.endpoint} onChange={(e) => up({ endpoint: e.target.value })} placeholder={endpointPh} {...rest} />
                              )}
                            </Field>
                            <Field label="Region" hint={regionHint}>
                              {({ id, ...rest }) => (
                                <Input id={id} value={d.region} onChange={(e) => up({ region: e.target.value })} placeholder={isOSS ? "cn-hangzhou" : "cn-beijing"} {...rest} />
                              )}
                            </Field>
                          </div>
                          <div className="grid gap-4 sm:grid-cols-2">
                            <Field label="Bucket" hint="私有读即可，上传对象经签名 URL 访问">
                              {({ id, ...rest }) => <Input id={id} value={d.bucket} onChange={(e) => up({ bucket: e.target.value })} placeholder="my-audio-bucket" {...rest} />}
                            </Field>
                            <Field label="Access Key" hint={akHint}>
                              {({ id, ...rest }) => <Input id={id} value={d.access_key} onChange={(e) => up({ access_key: e.target.value })} autoComplete="off" {...rest} />}
                            </Field>
                          </div>
                          <div className="grid gap-4 sm:grid-cols-2">
                            <Field label="Secret Key" hint={d.has_secret_key ? "当前已配置，留空表示不修改" : skHint}>
                              {({ id, ...rest }) => (
                                <SecretInput
                                  id={id}
                                  name="secret_key"
                                  value={d.secret_key}
                                  onChange={(e: ChangeEvent<HTMLInputElement>) => up({ secret_key: e.target.value })}
                                  placeholder="留空表示不修改"
                                  {...rest}
                                />
                              )}
                            </Field>
                          </div>
                          <Field label="对象前缀" hint="可留空；上传对象落在 前缀/日期/ 下">
                            {({ id, ...rest }) => <Input id={id} value={d.prefix} onChange={(e) => up({ prefix: e.target.value })} placeholder="voxbox" {...rest} />}
                          </Field>
                        </div>
                      );
                    })()
                  )}
                  <div className="flex flex-wrap items-center gap-3">
                    <Button type="submit" variant="primary" loading={saveStorage.isPending}>
                      保存存储配置
                    </Button>
                  </div>
                </form>
              </CardBody>
            </Card>
          )}

          <Card>
            <CardHeader title="连通性测试" icon={<PlugZap size={15} strokeWidth={1.75} />} />
            <CardBody className="space-y-3">
              <div className="flex flex-wrap items-center gap-3">
                <Button
                  variant="secondary"
                  onClick={() => test.mutate()}
                  loading={test.isPending}
                  icon={<PlugZap size={14} strokeWidth={1.75} />}
                >
                  测试全部凭证
                </Button>
                {test.data?.results?.map((r) => (
                  <ConnBadge key={r.name} result={{ ok: r.ok, message: `${CARD_NAMES[r.name] ?? r.name}：${r.message}` }} />
                ))}
                <ConnBadge
                  result={
                    test.data?.storage
                      ? { ok: test.data.storage.ok, message: `对象存储：${test.data.storage.message}` }
                      : undefined
                  }
                />
              </div>
              <p className="text-[11px] text-muted">
                语音 / 千问测试会各发起一次极短的合成请求（消耗少量额度）；MediaKit 测试只做鉴权探测；MVSep 测试验证 token 并回显今日免费额度；对象存储测试为桶探活（HeadBucket，不计费）。
              </p>
            </CardBody>
          </Card>
        </div>
      ) : (
        <div className="space-y-4">
          {local.map((p) => (
            <Card key={p.name}>
              <CardHeader
                title={p.title}
                icon={
                  p.name === "audiotool" ? (
                    <AudioLines size={15} strokeWidth={1.75} />
                  ) : (
                    <Server size={15} strokeWidth={1.75} />
                  )
                }
                aside={<span className="micro">{p.tools_count} 个工具</span>}
              />
              <CardBody className="space-y-2">
                <p className="text-xs text-muted">{p.description}</p>
                <p className="text-xs text-fg-2">本地运行，无需凭证、离线可用，共 {p.tools_count} 个工具。</p>
              </CardBody>
            </Card>
          ))}

          <Card>
            <CardHeader title="存储位置" icon={<FolderOpen size={15} strokeWidth={1.75} />} />
            <CardBody className="space-y-4">
              <div className="space-y-2">
                <MicroLabel>数据目录</MicroLabel>
                <p className="break-all font-mono text-xs text-fg-2">{data?.data_dir ?? "—"}</p>
                <p className="text-[11px] text-muted">
                  产物文件（音频、转写、字幕、对话稿）与任务数据库都保存在此目录；配置文件为
                  <code className="font-mono"> ~/.voxbox/config.yaml</code>（权限 0600）。
                </p>
              </div>
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="通知" icon={<Bell size={15} strokeWidth={1.75} />} />
            <CardBody className="space-y-3">
              <label className="flex cursor-pointer items-start gap-2 text-sm text-fg-2">
                <input
                  type="checkbox"
                  checked={notifyOn}
                  onChange={(e) => {
                    if (e.target.checked) {
                      if (!("Notification" in window)) {
                        toast({ tone: "error", title: "当前浏览器不支持系统通知" });
                        return;
                      }
                      void Notification.requestPermission().then((p) => {
                        if (p === "granted") {
                          localStorage.setItem("sysnotify", "on");
                          setNotifyOn(true);
                          toast({ tone: "ok", title: "系统通知已开启", description: "页面在后台时，任务终态会发系统通知" });
                        } else {
                          toast({ tone: "error", title: "浏览器拒绝了通知权限", description: "请在浏览器地址栏的站点设置中允许通知" });
                        }
                      });
                    } else {
                      localStorage.setItem("sysnotify", "off");
                      setNotifyOn(false);
                    }
                  }}
                  className="mt-0.5 size-4 cursor-pointer accent-accent"
                />
                <span>
                  任务终态系统通知
                  <span className="block text-[11px] text-muted">
                    页面在后台时，任务完成/失败/取消发系统级通知（需要浏览器授权）。
                    当前权限：{notifyPermission}
                  </span>
                </span>
              </label>
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="API Token" icon={<KeyRound size={15} strokeWidth={1.75} />} />
            <CardBody className="space-y-3">
              <div className="flex flex-wrap items-center gap-3">
                <Button
                  variant="secondary"
                  onClick={() => rotate.mutate()}
                  loading={rotate.isPending}
                  icon={<KeyRound size={14} strokeWidth={1.75} />}
                >
                  {me?.has_token === false ? "生成 Token" : "重置 Token"}
                </Button>
                {newToken && (
                  <code className="block max-w-xl flex-1 overflow-x-auto rounded-[var(--radius-sm)] border border-line bg-inset px-3 py-2 font-mono text-xs text-fg">
                    {newToken}
                  </code>
                )}
              </div>
              <p className="text-[11px] text-muted">
                {newToken
                  ? "Token 仅此一次完整显示，请立即复制保存；再次重置会使旧 Token 立即失效。"
                  : "供 MCP HTTP 与 CLI 远程调用鉴权（Authorization: Bearer tbx_...），服务端只保存哈希，丢失只能重置。"}
              </p>
            </CardBody>
          </Card>
        </div>
      )}
    </>
  );
}
