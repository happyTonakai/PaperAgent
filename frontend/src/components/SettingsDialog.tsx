import { useState, useEffect, useRef } from 'react'
import { X, Loader2, Save } from 'lucide-react'
import { useAppStore } from '../stores/appStore'
import { toast } from 'sonner'

type Tab = 'config' | 'prompts'

interface ConfigData {
  api: { base_url: string; api_key: string; api_key_source: string; default_model: string }
  obsidian: { vault_path: string; export_folder: string }
  ui: { min_recent_rounds: number; max_input_tokens: number }
  feishu?: { enabled: boolean; app_id: string; app_secret: string }
}

interface ConfigForm {
  api_key: string; base_url: string; default_model: string
  min_recent_rounds: string; max_input_tokens: string; obsidian_vault_path: string; obsidian_export_folder: string
  feishu_enabled: boolean; feishu_app_id: string; feishu_app_secret: string
}

interface PromptInfo { name: string; content: string; source: string }

const promptLabels: Record<string, string> = {
  system: '系统提示词 (system)',
  heavy: '初始总结 (heavy)',
  light: '对话问答 (light)',
  summarize: '对话总结 (summarize)',
}

export function SettingsDialog() {
  const { isSettingsOpen, setSettingsOpen } = useAppStore()
  const [tab, setTab] = useState<Tab>('config')
  const [visible, setVisible] = useState(false)
  const [closing, setClosing] = useState(false)

  // Animate in/out: state transitions
  useEffect(() => {
    if (isSettingsOpen) {
      setVisible(true)
      setClosing(false)
    } else if (visible && !closing) {
      setClosing(true)
    }
  }, [isSettingsOpen, visible, closing])

  // Delayed unmount after close animation plays
  useEffect(() => {
    if (!closing) return
    const timer = setTimeout(() => setVisible(false), 200)
    return () => clearTimeout(timer)
  }, [closing])

  const pointerDownRef = useRef<EventTarget | null>(null)
  const close = () => setSettingsOpen(false)

  // Config
  const [config, setConfig] = useState<ConfigData | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [form, setForm] = useState<ConfigForm>({ api_key: '', base_url: '', default_model: '', min_recent_rounds: '2', max_input_tokens: '30000', obsidian_vault_path: '', obsidian_export_folder: '', feishu_enabled: false, feishu_app_id: '', feishu_app_secret: '' })
  const [apiKeyDirty, setApiKeyDirty] = useState(false)

  // Prompts
  const [prompts, setPrompts] = useState<PromptInfo[]>([])
  const [promptEdits, setPromptEdits] = useState<Record<string, string>>({})
  const [promptsLoading, setPromptsLoading] = useState(false)
  const [promptsSaving, setPromptsSaving] = useState(false)

  // Feishu status
  const [feishuStatus, setFeishuStatus] = useState<{ connected: boolean; enabled: boolean; last_error?: string } | null>(null)

  // Close on Escape key
  useEffect(() => {
    if (!isSettingsOpen) return
    const handler = (e: KeyboardEvent) => { if (e.key === 'Escape') close() }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [isSettingsOpen, setSettingsOpen])

  useEffect(() => {
    if (!isSettingsOpen) return
    setLoading(true)
    fetch('/api/config')
      .then((r) => r.json())
      .then((data: ConfigData) => {
        setConfig(data)
        setForm({ api_key: '', base_url: data.api.base_url, default_model: data.api.default_model, min_recent_rounds: String(data.ui.min_recent_rounds), max_input_tokens: String(data.ui.max_input_tokens), obsidian_vault_path: data.obsidian.vault_path, obsidian_export_folder: data.obsidian.export_folder, feishu_enabled: data.feishu?.enabled ?? false, feishu_app_id: '', feishu_app_secret: '' })
        setApiKeyDirty(false)
      })
      .catch((err) => toast.error('加载配置失败: ' + (err instanceof Error ? err.message : '未知错误')))
      .finally(() => setLoading(false))

    setPromptsLoading(true)
    fetch('/api/prompts')
      .then((r) => r.json())
      .then((data: PromptInfo[]) => {
        setPrompts(data)
        const edits: Record<string, string> = {}
        data.forEach((p) => { edits[p.name] = p.content })
        setPromptEdits(edits)
      })
      .catch((err) => toast.error('加载提示词失败: ' + (err instanceof Error ? err.message : '未知错误')))
      .finally(() => setPromptsLoading(false))

    // Fetch feishu status
    fetch('/api/feishu/status')
      .then((r) => r.json())
      .then((data) => setFeishuStatus(data))
      .catch(() => {})
  }, [isSettingsOpen])

  if (!visible) return null

  const handleSaveConfig = async () => {
    setSaving(true)
    const body: Record<string, unknown> = {}
    if (apiKeyDirty && form.api_key.trim()) body['api_key'] = form.api_key.trim()
    if (form.base_url !== config?.api.base_url) body['base_url'] = form.base_url
    if (form.default_model !== config?.api.default_model) body['default_model'] = form.default_model
    if (String(form.min_recent_rounds) !== String(config?.ui.min_recent_rounds)) body['min_recent_rounds'] = Number(form.min_recent_rounds)
    if (String(form.max_input_tokens) !== String(config?.ui.max_input_tokens)) body['max_input_tokens'] = Number(form.max_input_tokens)
    if (form.obsidian_vault_path !== config?.obsidian.vault_path) body['obsidian_vault_path'] = form.obsidian_vault_path
    if (form.obsidian_export_folder !== config?.obsidian.export_folder) body['obsidian_export_folder'] = form.obsidian_export_folder
    if (form.feishu_enabled !== (config?.feishu?.enabled ?? false)) body['feishu_enabled'] = form.feishu_enabled
    if (form.feishu_app_id && form.feishu_app_id !== '••••••' && form.feishu_app_id !== config?.feishu?.app_id) body['feishu_app_id'] = form.feishu_app_id
    if (form.feishu_app_secret && form.feishu_app_secret !== '••••••' && form.feishu_app_secret !== config?.feishu?.app_secret) body['feishu_app_secret'] = form.feishu_app_secret
    if (Object.keys(body).length === 0) { toast('没有需要保存的更改'); setSaving(false); close(); return }
    try {
      const res = await fetch('/api/config', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
      if (!res.ok) throw new Error((await res.json().catch(() => ({ error: '保存失败' })) as { error?: string }).error)
      toast.success('配置已保存')
      setApiKeyDirty(false)
      if (body.api_key) setForm((f) => ({ ...f, api_key: '' }))
      close()
    } catch (err) { toast.error('保存失败: ' + (err instanceof Error ? err.message : '未知错误')) }
    finally { setSaving(false) }
  }

  const handleSavePrompts = async () => {
    setPromptsSaving(true)
    const changed = prompts.filter((p) => promptEdits[p.name] !== p.content)
    if (changed.length === 0) { toast('没有需要保存的更改'); setPromptsSaving(false); close(); return }
    try {
      const body = changed.map((p) => ({ name: p.name, content: promptEdits[p.name] }))
      const res = await fetch('/api/prompts', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
      if (!res.ok) throw new Error((await res.json().catch(() => ({ error: '保存失败' })) as { error?: string }).error)
      toast.success('提示词已保存')
      setPrompts((prev) => prev.map((p) => ({ ...p, content: promptEdits[p.name], source: 'custom' as const })))
      close()
    } catch (err) { toast.error('保存失败: ' + (err instanceof Error ? err.message : '未知错误')) }
    finally { setPromptsSaving(false) }
  }

  const updateForm = (key: keyof ConfigForm, value: string) => { setForm((f) => ({ ...f, [key]: value })); if (key === 'api_key') setApiKeyDirty(true) }

  const inputClass = 'w-full px-3 py-2 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 text-sm outline-none focus:ring-2 focus:ring-blue-500'
  const tabClass = (t: Tab) => `flex-1 text-center py-2 text-sm font-medium rounded-lg transition-colors ${tab === t ? 'bg-white dark:bg-gray-700 shadow-sm text-gray-900 dark:text-gray-100' : 'text-gray-500 hover:text-gray-700 dark:hover:text-gray-300'}`

  return (
    <div
      className={`fixed inset-0 z-50 flex items-center justify-center bg-black/40 ${closing ? 'animate-fade-out' : 'animate-fade-in'}`}
      onPointerDown={(e) => { pointerDownRef.current = e.target }}
      onClick={(e) => { if (e.target === e.currentTarget && pointerDownRef.current === e.currentTarget) close() }}
    >
      <div className={`bg-white dark:bg-gray-900 rounded-xl shadow-2xl w-full max-w-lg mx-4 max-h-[90vh] overflow-hidden flex flex-col ${closing ? 'animate-scale-out' : 'animate-scale-in'}`}>
        <div className="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-gray-800">
          <h2 className="text-sm font-semibold">⚙️ 设置</h2>
          <button onClick={() => close()} className="p-1 rounded hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"><X size={16} /></button>
        </div>

        <div className="flex gap-1 px-4 py-2 bg-gray-100 dark:bg-gray-800">
          <button onClick={() => setTab('config')} className={tabClass('config')}>API 配置</button>
          <button onClick={() => setTab('prompts')} className={tabClass('prompts')}>提示词模板</button>
        </div>

        <div className="flex-1 overflow-y-auto p-4">
          {loading ? <div className="flex items-center justify-center py-8"><Loader2 size={24} className="animate-spin text-gray-400" /></div>
          : tab === 'config' ? (
            <div className="space-y-4">
              <fieldset className="space-y-3">
                <legend className="text-xs font-medium text-gray-500 dark:text-gray-400">API 配置</legend>
                <div>
                  <label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">API Key {config && <span className="ml-1 text-gray-400">({config.api.api_key_source === 'config' ? '文件配置' : '环境变量'}: {config.api.api_key})</span>}</label>
                  <input type="password" value={form.api_key} onChange={(e) => updateForm('api_key', e.target.value)} placeholder={config ? '输入新密钥以替换...' : ''} className={inputClass} />
                  {!apiKeyDirty && <p className="text-xs text-gray-400 mt-1">输入新密钥以替换。全大写名称（如 OPENAI_API_KEY）则引用环境变量</p>}
                </div>
                <div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">Base URL</label><input type="text" value={form.base_url} onChange={(e) => updateForm('base_url', e.target.value)} className={inputClass} /></div>
                <div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">Default Model</label><input type="text" value={form.default_model} onChange={(e) => updateForm('default_model', e.target.value)} className={inputClass} /></div>
                <div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">最小保留轮数</label><input type="number" value={form.min_recent_rounds} onChange={(e) => updateForm('min_recent_rounds', e.target.value)} min={1} max={50} className={inputClass} /><p className="text-xs text-gray-400 mt-1">当输入 token 接近上限时，至少保留此轮数的最近上下文</p></div>
                <div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">最大输入 Token</label><input type="number" value={form.max_input_tokens} onChange={(e) => updateForm('max_input_tokens', e.target.value)} min={1000} max={200000} step={1000} className={inputClass} /><p className="text-xs text-gray-400 mt-1">输入超过此值时自动截断上下文到最小轮数（默认 30000）</p></div>
              </fieldset>
              <hr className="border-gray-200 dark:border-gray-800" />
              <fieldset className="space-y-3">
                <legend className="text-xs font-medium text-gray-500 dark:text-gray-400">Obsidian 导出</legend>
                <div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">Vault 路径</label><input type="text" value={form.obsidian_vault_path} onChange={(e) => updateForm('obsidian_vault_path', e.target.value)} placeholder="~/Documents/Obsidian/MyVault" className={inputClass} /></div>
                <div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">导出文件夹</label><input type="text" value={form.obsidian_export_folder} onChange={(e) => updateForm('obsidian_export_folder', e.target.value)} placeholder="Papers" className={inputClass} /></div>
              </fieldset>
              <hr className="border-gray-200 dark:border-gray-800" />
              <fieldset className="space-y-3">
                <legend className="text-xs font-medium text-gray-500 dark:text-gray-400">飞书 Bot 配置</legend>
                <div className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    id="feishu-enabled"
                    checked={form.feishu_enabled}
                    onChange={(e) => setForm((f) => ({ ...f, feishu_enabled: e.target.checked }))}
                    className="w-4 h-4 rounded border-gray-300"
                  />
                  <label htmlFor="feishu-enabled" className="text-xs text-gray-500 dark:text-gray-400">启用飞书 Bot</label>
                  {feishuStatus && feishuStatus.enabled && (
                    <span className={`inline-flex items-center gap-1 text-xs px-1.5 py-0.5 rounded ${feishuStatus.connected ? 'bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300' : 'bg-red-100 text-red-700 dark:bg-red-900 dark:text-red-300'}`}>
                      <span className={`w-1.5 h-1.5 rounded-full ${feishuStatus.connected ? 'bg-green-500' : 'bg-red-500'}`} />
                      {feishuStatus.connected ? '已连接' : '未连接'}
                    </span>
                  )}
                </div>
                {feishuStatus && feishuStatus.last_error && (
                  <p className="text-xs text-red-500">错误: {feishuStatus.last_error}</p>
                )}
                <div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">App ID {config?.feishu?.app_id && <span className="ml-1 text-gray-400">(当前: {config.feishu.app_id})</span>}</label><input type="text" value={form.feishu_app_id} onChange={(e) => updateForm('feishu_app_id', e.target.value)} placeholder="cli_xxxxx" className={inputClass} /></div>
                <div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">App Secret {config?.feishu?.app_secret && <span className="ml-1 text-gray-400">(当前: {config.feishu.app_secret})</span>}</label><input type="password" value={form.feishu_app_secret} onChange={(e) => updateForm('feishu_app_secret', e.target.value)} placeholder={config?.feishu?.app_secret ? '输入新 Secret 以替换...' : '飞书应用 Secret'} className={inputClass} /></div>
                <p className="text-xs text-gray-400">保存后自动重连飞书。请在飞书开放平台开启机器人能力并订阅「消息和群组」事件。</p>
              </fieldset>
            </div>
          ) : (promptsLoading ? <div className="flex items-center justify-center py-8"><Loader2 size={24} className="animate-spin text-gray-400" /></div> : (
            <div className="space-y-5">
              {prompts.map((p) => (
                <div key={p.name}>
                  <div className="flex items-center gap-2 mb-1.5">
                    <label className="text-xs font-medium text-gray-500 dark:text-gray-400">{promptLabels[p.name] || p.name}</label>
                    {p.source === 'custom' && <span className="text-xs text-pink-500">已自定义</span>}
                  </div>
                  <textarea
                    value={promptEdits[p.name] || ''}
                    onChange={(e) => setPromptEdits((prev) => ({ ...prev, [p.name]: e.target.value }))}
                    className="w-full px-3 py-2 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 text-xs outline-none focus:ring-2 focus:ring-blue-500 font-mono resize-y"
                    rows={p.name === 'system' ? 6 : 12}
                  />
                </div>
              ))}
            </div>
          ))}
        </div>

        {!loading && (
          <div className="px-4 py-3 border-t border-gray-200 dark:border-gray-800 flex justify-between gap-2">
            <p className="text-xs text-gray-400 self-center">{tab === 'prompts' ? '提示词保存后立即生效' : '配置保存在 ~/.config/paperagent/config.yaml'}</p>
            <div className="flex gap-2">
              <button onClick={() => close()} className="px-4 py-2 text-sm rounded-lg border border-gray-300 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800">关闭</button>
              <button onClick={tab === 'prompts' ? handleSavePrompts : handleSaveConfig} disabled={tab === 'prompts' ? promptsSaving : saving} className="px-4 py-2 text-sm rounded-lg bg-blue-500 hover:bg-blue-600 disabled:bg-gray-300 dark:disabled:bg-gray-700 text-white flex items-center gap-1.5">
                {(tab === 'prompts' ? promptsSaving : saving) && <Loader2 size={14} className="animate-spin" />}<Save size={14} />保存
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
