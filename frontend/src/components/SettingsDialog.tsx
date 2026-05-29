import { useState, useEffect } from 'react'
import { X, Sun, Moon, Monitor, Loader2, Save } from 'lucide-react'
import { useAppStore } from '../stores/appStore'
import { toast } from 'sonner'
import type { Theme } from '../types'

const themeOptions: { value: Theme; label: string; icon: typeof Sun }[] = [
  { value: 'light', label: '浅色', icon: Sun },
  { value: 'dark', label: '深色', icon: Moon },
  { value: 'system', label: '跟随系统', icon: Monitor },
]

interface ConfigData {
  api: {
    base_url: string
    api_key: string
    api_key_source: string
    default_model: string
    light_model: string
  }
  obsidian: {
    vault_path: string
    export_folder: string
  }
  ui: {
    max_recent_rounds: number
  }
}

interface ConfigForm {
  api_key: string
  base_url: string
  default_model: string
  light_model: string
  max_recent_rounds: string
  obsidian_vault_path: string
  obsidian_export_folder: string
}

export function SettingsDialog() {
  const { isSettingsOpen, setSettingsOpen, theme, setTheme } = useAppStore()

  const [config, setConfig] = useState<ConfigData | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  // Editable form fields
  const [form, setForm] = useState<ConfigForm>({
    api_key: '',
    base_url: '',
    default_model: '',
    light_model: '',
    max_recent_rounds: '5',
    obsidian_vault_path: '',
    obsidian_export_folder: '',
  })

  const [apiKeyDirty, setApiKeyDirty] = useState(false)

  useEffect(() => {
    if (!isSettingsOpen) return

    setLoading(true)
    fetch('/api/config')
      .then((res) => res.json())
      .then((data: ConfigData) => {
        setConfig(data)
        setForm({
          api_key: '',
          base_url: data.api.base_url,
          default_model: data.api.default_model,
          light_model: data.api.light_model,
          max_recent_rounds: String(data.ui.max_recent_rounds),
          obsidian_vault_path: data.obsidian.vault_path,
          obsidian_export_folder: data.obsidian.export_folder,
        })
        setApiKeyDirty(false)
      })
      .catch((err) => {
        toast.error('加载配置失败: ' + (err instanceof Error ? err.message : '未知错误'))
      })
      .finally(() => setLoading(false))
  }, [isSettingsOpen])

  if (!isSettingsOpen) return null

  const handleSave = async () => {
    setSaving(true)
    const body: Record<string, unknown> = {}

    if (apiKeyDirty && form.api_key.trim()) {
      body['api_key'] = form.api_key.trim()
    }
    if (form.base_url !== config?.api.base_url) body['base_url'] = form.base_url
    if (form.default_model !== config?.api.default_model) body['default_model'] = form.default_model
    if (form.light_model !== config?.api.light_model) body['light_model'] = form.light_model
    if (String(form.max_recent_rounds) !== String(config?.ui.max_recent_rounds)) {
      body['max_recent_rounds'] = Number(form.max_recent_rounds)
    }
    if (form.obsidian_vault_path !== config?.obsidian.vault_path) body['obsidian_vault_path'] = form.obsidian_vault_path
    if (form.obsidian_export_folder !== config?.obsidian.export_folder) body['obsidian_export_folder'] = form.obsidian_export_folder

    if (Object.keys(body).length === 0) {
      toast('没有需要保存的更改')
      setSaving(false)
      return
    }

    try {
      const res = await fetch('/api/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: '保存失败' }))
        throw new Error((err as { error?: string }).error || `HTTP ${res.status}`)
      }
      toast.success('配置已保存')
      setApiKeyDirty(false)
      if (body.api_key) {
        setForm((f) => ({ ...f, api_key: '' }))
      }
    } catch (err) {
      toast.error('保存失败: ' + (err instanceof Error ? err.message : '未知错误'))
    } finally {
      setSaving(false)
    }
  }

  const updateForm = (key: keyof ConfigForm, value: string) => {
    setForm((f) => ({ ...f, [key]: value }))
    if (key === 'api_key') setApiKeyDirty(true)
  }

  const inputClass = 'w-full px-3 py-2 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 text-sm outline-none focus:ring-2 focus:ring-blue-500'

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="bg-white dark:bg-gray-900 rounded-xl shadow-2xl w-full max-w-lg mx-4 max-h-[90vh] overflow-y-auto">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-gray-800 sticky top-0 bg-white dark:bg-gray-900">
          <h2 className="text-sm font-semibold">⚙️ 设置</h2>
          <button
            onClick={() => setSettingsOpen(false)}
            className="p-1 rounded hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
          >
            <X size={16} />
          </button>
        </div>

        {/* Body */}
        <div className="p-4 space-y-5">
          {loading ? (
            <div className="flex items-center justify-center py-8">
              <Loader2 size={24} className="animate-spin text-gray-400" />
            </div>
          ) : (
            <>
              {/* Theme */}
              <fieldset>
                <legend className="text-xs font-medium text-gray-500 dark:text-gray-400 mb-2">外观主题</legend>
                <div className="flex gap-1 bg-gray-100 dark:bg-gray-800 rounded-lg p-0.5">
                  {themeOptions.map((opt) => {
                    const Icon = opt.icon
                    return (
                      <button
                        key={opt.value}
                        onClick={() => setTheme(opt.value)}
                        className={`flex-1 flex items-center justify-center gap-1.5 px-2 py-1.5 rounded-md text-xs transition-colors ${
                          theme === opt.value
                            ? 'bg-white dark:bg-gray-700 shadow-sm text-gray-900 dark:text-gray-100'
                            : 'text-gray-500 hover:text-gray-700 dark:hover:text-gray-300'
                        }`}
                      >
                        <Icon size={14} />
                        {opt.label}
                      </button>
                    )
                  })}
                </div>
              </fieldset>

              {/* Divider */}
              <hr className="border-gray-200 dark:border-gray-800" />

              {/* API Config */}
              <fieldset className="space-y-3">
                <legend className="text-xs font-medium text-gray-500 dark:text-gray-400">API 配置</legend>

                {/* API Key */}
                <div>
                  <label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">
                    API Key
                    {config && (
                      <span className="ml-1 text-gray-400">
                        ({config.api.api_key_source === 'config' ? '文件配置' : '环境变量'}: {config.api.api_key})
                      </span>
                    )}
                  </label>
                  <input
                    type="password"
                    value={form.api_key}
                    onChange={(e) => updateForm('api_key', e.target.value)}
                    placeholder={config ? '输入新密钥以替换...' : ''}
                    className={inputClass}
                  />
                  {!apiKeyDirty && <p className="text-xs text-gray-400 mt-1">输入新密钥以替换当前配置。输入全大写名称（如 OPENAI_API_KEY）则引用环境变量</p>}
                </div>

                {/* Base URL */}
                <div>
                  <label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">Base URL</label>
                  <input
                    type="text"
                    value={form.base_url}
                    onChange={(e) => updateForm('base_url', e.target.value)}
                    className={inputClass}
                  />
                </div>

                {/* Default Model */}
                <div>
                  <label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">Default Model</label>
                  <input
                    type="text"
                    value={form.default_model}
                    onChange={(e) => updateForm('default_model', e.target.value)}
                    className={inputClass}
                  />
                </div>

                {/* Light Model */}
                <div>
                  <label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">Light Model</label>
                  <input
                    type="text"
                    value={form.light_model}
                    onChange={(e) => updateForm('light_model', e.target.value)}
                    className={inputClass}
                  />
                </div>

                {/* Max Rounds */}
                <div>
                  <label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">Max Recent Rounds</label>
                  <input
                    type="number"
                    value={form.max_recent_rounds}
                    onChange={(e) => updateForm('max_recent_rounds', e.target.value)}
                    min={1}
                    max={50}
                    className={inputClass}
                  />
                </div>
              </fieldset>

              {/* Divider */}
              <hr className="border-gray-200 dark:border-gray-800" />

              {/* Obsidian Config */}
              <fieldset className="space-y-3">
                <legend className="text-xs font-medium text-gray-500 dark:text-gray-400">Obsidian 导出</legend>

                <div>
                  <label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">Vault 路径</label>
                  <input
                    type="text"
                    value={form.obsidian_vault_path}
                    onChange={(e) => updateForm('obsidian_vault_path', e.target.value)}
                    placeholder="~/Documents/Obsidian/MyVault"
                    className={inputClass}
                  />
                </div>

                <div>
                  <label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">导出文件夹</label>
                  <input
                    type="text"
                    value={form.obsidian_export_folder}
                    onChange={(e) => updateForm('obsidian_export_folder', e.target.value)}
                    placeholder="Papers"
                    className={inputClass}
                  />
                </div>
              </fieldset>

              {/* Prompt Info */}
              <div>
                <p className="text-xs text-gray-400 dark:text-gray-500">
                  Prompt 模板在 <code className="text-pink-500">~/.paperpaper/prompts/</code> 目录下自定义。
                  创建 <code className="text-pink-500">heavy.txt</code>、<code className="text-pink-500">light.txt</code> 或
                  <code className="text-pink-500">digest.txt</code> 文件即可覆盖内置模板。
                </p>
              </div>
            </>
          )}
        </div>

        {/* Footer */}
        {!loading && (
          <div className="px-4 py-3 border-t border-gray-200 dark:border-gray-800 flex justify-end gap-2">
            <button
              onClick={() => setSettingsOpen(false)}
              className="px-4 py-2 text-sm rounded-lg border border-gray-300 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800 transition-colors"
            >
              关闭
            </button>
            <button
              onClick={handleSave}
              disabled={saving}
              className="px-4 py-2 text-sm rounded-lg bg-blue-500 hover:bg-blue-600 disabled:bg-gray-300 dark:disabled:bg-gray-700 text-white flex items-center gap-1.5 transition-colors"
            >
              {saving && <Loader2 size={14} className="animate-spin" />}
              {saving ? '保存中...' : (
                <>
                  <Save size={14} />
                  保存配置
                </>
              )}
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
