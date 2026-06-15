import { useState, useEffect, useRef } from 'react'
import { X, Loader2, Save } from 'lucide-react'
import { useAppStore } from '../stores/appStore'
import { toast } from 'sonner'

type Tab = 'config' | 'prompts' | 'recommend' | 'preferences'

interface ConfigData {
	api: { base_url: string; api_key: string; api_key_source: string; default_model: string }
	obsidian: { vault_path: string; export_folder: string }
	ui: { min_recent_rounds: number; max_input_tokens: number }
	feishu?: { enabled: boolean; app_id: string; app_secret: string; daily_recommend_chat_id: string }
}

interface ConfigForm {
	api_key: string; base_url: string; default_model: string
	min_recent_rounds: string; max_input_tokens: string; obsidian_vault_path: string; obsidian_export_folder: string
	feishu_enabled: boolean; feishu_app_id: string; feishu_app_secret: string; feishu_daily_recommend_chat_id: string
}

interface PromptInfo { name: string; content: string; source: string }

interface PreferencesData { content: string }

interface SchedulerStatus {
	is_running: boolean
	last_run: string
	last_error: string
	next_run: string
	scheduled: string
	daily_count: number
	push_to_feishu: boolean
}

interface RecommendConfigData {
	recommend: { daily_papers: number; scoring_batch_size: number; scheduled_time: string; push_to_feishu: boolean; diversity_ratio: number }
	arxiv_categories: string[]
	api: {
		scoring: { base_url: string; api_key: string; model: string } | null
		translation: { base_url: string; api_key: string; model: string } | null
	}
}

const promptLabels: Record<string, string> = {
	heavy: '初始总结 (heavy)',
	light: '对话问答 (light)',
	summarize: '对话总结 (summarize)',
	scoring: '论文评分 (scoring)',
	'update-prefs': '偏好更新 (update-prefs)',
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
	const [form, setForm] = useState<ConfigForm>({ api_key: '', base_url: '', default_model: '', min_recent_rounds: '2', max_input_tokens: '30000', obsidian_vault_path: '', obsidian_export_folder: '', feishu_enabled: false, feishu_app_id: '', feishu_app_secret: '', feishu_daily_recommend_chat_id: '' })
	const [apiKeyDirty, setApiKeyDirty] = useState(false)

	// Prompts
	const [prompts, setPrompts] = useState<PromptInfo[]>([])
	const [promptEdits, setPromptEdits] = useState<Record<string, string>>({})
	const [promptsLoading, setPromptsLoading] = useState(false)
	const [promptsSaving, setPromptsSaving] = useState(false)

	// Feishu status
	const [feishuStatus, setFeishuStatus] = useState<{ connected: boolean; enabled: boolean; last_error?: string } | null>(null)

	// Recommend settings
	const [recommendConfig, setRecommendConfig] = useState<RecommendConfigData | null>(null)
	const [recLoading, setRecLoading] = useState(false)
	const [recSaving, setRecSaving] = useState(false)

	// Preferences (recommend) — free-form markdown edited directly
	const [preferencesContent, setPreferencesContent] = useState('')
	const [preferencesOriginal, setPreferencesOriginal] = useState('')
	const [preferencesLoading, setPreferencesLoading] = useState(false)
	const [preferencesSaving, setPreferencesSaving] = useState(false)

	// Scheduler status
	const [schedulerStatus, setSchedulerStatus] = useState<SchedulerStatus | null>(null)

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
				setForm({ api_key: '', base_url: data.api.base_url, default_model: data.api.default_model, min_recent_rounds: String(data.ui.min_recent_rounds), max_input_tokens: String(data.ui.max_input_tokens), obsidian_vault_path: data.obsidian.vault_path, obsidian_export_folder: data.obsidian.export_folder, feishu_enabled: data.feishu?.enabled ?? false, feishu_app_id: '', feishu_app_secret: '', feishu_daily_recommend_chat_id: data.feishu?.daily_recommend_chat_id ?? '' })
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
			.catch(() => { })

		// Fetch recommend config
		setRecLoading(true)
		fetch('/api/recommend/config')
			.then((r) => r.json())
			.then((data: RecommendConfigData) => setRecommendConfig(data))
			.catch(() => { })
			.finally(() => setRecLoading(false))

		// Fetch recommend preferences
		setPreferencesLoading(true)
		fetch('/api/recommend/preferences')
			.then((r) => r.json())
			.then((data: PreferencesData) => {
				const c = data.content ?? ''
				setPreferencesContent(c)
				setPreferencesOriginal(c)
			})
			.catch(() => { })
			.finally(() => setPreferencesLoading(false))

		// Fetch scheduler status
		fetch('/api/recommend/scheduler-status')
			.then((r) => r.json())
			.then(setSchedulerStatus)
			.catch(() => { })
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
		if (form.feishu_daily_recommend_chat_id !== (config?.feishu?.daily_recommend_chat_id ?? '')) body['feishu_daily_recommend_chat_id'] = form.feishu_daily_recommend_chat_id
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

	const handleSaveRecommend = async () => {
		if (!recommendConfig) return
		setRecSaving(true)
		try {
			const body: Record<string, unknown> = {}
			const scoring = recommendConfig.api.scoring
			const translation = recommendConfig.api.translation
			const apiBody: Record<string, unknown> = {}

			if (scoring) {
				const scoringBody: Record<string, string> = {
					base_url: scoring.base_url,
					model: scoring.model,
				}
				// Only send api_key if user actually changed it (not the masked placeholder)
				if (scoring.api_key && !scoring.api_key.startsWith('\u2022')) {
					scoringBody.api_key = scoring.api_key
				}
				apiBody.scoring = scoringBody
			}

			// Translation is optional. Send null to disable, object to enable/update.
			if (translation) {
				const allEmpty = !translation.base_url.trim() && !translation.model.trim()
					&& (!translation.api_key || translation.api_key.startsWith('\u2022'))
				if (allEmpty) {
					apiBody.translation = null
				} else {
					const translationBody: Record<string, string> = {
						base_url: translation.base_url,
						model: translation.model,
					}
					if (translation.api_key && !translation.api_key.startsWith('\u2022')) {
						translationBody.api_key = translation.api_key
					}
					apiBody.translation = translationBody
				}
			} else {
				apiBody.translation = null
			}

			body.api = apiBody
			body.recommend = recommendConfig.recommend
			body.arxiv_categories = recommendConfig.arxiv_categories
			const res = await fetch('/api/recommend/config', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
			if (!res.ok) throw new Error((await res.json().catch(() => ({ error: '保存失败' })) as { error?: string }).error)
			toast.success('推荐配置已保存')
			// Refresh scheduler status
			fetch('/api/recommend/scheduler-status')
				.then((r) => r.json())
				.then(setSchedulerStatus)
				.catch(() => { })
			close()
		} catch (err) { toast.error('保存失败: ' + (err instanceof Error ? err.message : '未知错误')) }
		finally { setRecSaving(false) }
	}

	const handleSavePreferences = async () => {
		if (preferencesContent === preferencesOriginal) {
			toast('没有需要保存的更改')
			close()
			return
		}
		setPreferencesSaving(true)
		try {
			const res = await fetch('/api/recommend/preferences', {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ content: preferencesContent }),
			})
			if (!res.ok) throw new Error((await res.json().catch(() => ({ error: '保存失败' })) as { error?: string }).error)
			toast.success('偏好已保存')
			setPreferencesOriginal(preferencesContent)
			close()
		} catch (err) {
			toast.error('保存失败: ' + (err instanceof Error ? err.message : '未知错误'))
		} finally {
			setPreferencesSaving(false)
		}
	}

	const updateForm = (key: keyof ConfigForm, value: string) => { setForm((f) => ({ ...f, [key]: value })); if (key === 'api_key') setApiKeyDirty(true) }

	const updateTranslation = (field: 'base_url' | 'api_key' | 'model', value: string) => {
		setRecommendConfig(prev => {
			if (!prev) return prev
			const current = prev.api.translation ?? { base_url: '', api_key: '', model: '' }
			return { ...prev, api: { ...prev.api, translation: { ...current, [field]: value } } }
		})
	}

	const inputClass = 'w-full px-3 py-2 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 text-sm outline-none focus:ring-2 focus:ring-blue-500'
	const tabClass = (t: Tab) => `px-3 py-2 text-sm font-medium rounded-lg transition-colors ${tab === t ? 'bg-white dark:bg-gray-700 shadow-sm text-gray-900 dark:text-gray-100' : 'text-gray-500 hover:text-gray-700 dark:hover:text-gray-300'}`

	// Panel content for each tab (factored out to keep the ternary chain readable).
	const renderConfig = () => (
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
					<input type="checkbox" id="feishu-enabled" checked={form.feishu_enabled} onChange={(e) => setForm((f) => ({ ...f, feishu_enabled: e.target.checked }))} className="w-4 h-4 rounded border-gray-300" />
					<label htmlFor="feishu-enabled" className="text-xs text-gray-500 dark:text-gray-400">启用飞书 Bot</label>
					{feishuStatus && feishuStatus.enabled && (
						<span className={`inline-flex items-center gap-1 text-xs px-1.5 py-0.5 rounded ${feishuStatus.connected ? 'bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300' : 'bg-red-100 text-red-700 dark:bg-red-900 dark:text-red-300'}`}>
							<span className={`w-1.5 h-1.5 rounded-full ${feishuStatus.connected ? 'bg-green-500' : 'bg-red-500'}`} />
							{feishuStatus.connected ? '已连接' : '未连接'}
						</span>
					)}
				</div>
				{feishuStatus && feishuStatus.last_error && (<p className="text-xs text-red-500">错误: {feishuStatus.last_error}</p>)}
				<div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">App ID {config?.feishu?.app_id && <span className="ml-1 text-gray-400">(当前: {config.feishu.app_id})</span>}</label><input type="text" value={form.feishu_app_id} onChange={(e) => updateForm('feishu_app_id', e.target.value)} placeholder="cli_xxxxx" className={inputClass} /></div>
				<div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">App Secret {config?.feishu?.app_secret && <span className="ml-1 text-gray-400">(当前: {config.feishu.app_secret})</span>}</label><input type="password" value={form.feishu_app_secret} onChange={(e) => updateForm('feishu_app_secret', e.target.value)} placeholder={config?.feishu?.app_secret ? '输入新 Secret 以替换...' : '飞书应用 Secret'} className={inputClass} /></div>
				<div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">每日推荐 Chat ID {config?.feishu?.daily_recommend_chat_id && <span className="ml-1 text-gray-400">(当前: {config.feishu.daily_recommend_chat_id})</span>}</label><input type="text" value={form.feishu_daily_recommend_chat_id} onChange={(e) => updateForm('feishu_daily_recommend_chat_id', e.target.value)} placeholder="oc_xxxxxxxxx" className={inputClass} /></div>
				<p className="text-xs text-gray-400">保存后自动重连飞书。请在飞书开放平台开启机器人能力并订阅「消息和群组」事件。</p>
			</fieldset>
		</div>
	)

	const renderRecommend = () => recLoading ? <div className="flex items-center justify-center py-8"><Loader2 size={24} className="animate-spin text-gray-400" /></div> : (
		<div className="space-y-4">
			{/* Scheduler status */}
			{schedulerStatus && (
				<div className="rounded-lg border border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 p-3 space-y-1.5">
					<div className="text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">⏱ 定时调度状态</div>
					<div className="flex items-center gap-2 text-xs">
						<span className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded ${schedulerStatus.is_running ? 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900 dark:text-yellow-300' : 'bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300'}`}>
							<span className={`w-1.5 h-1.5 rounded-full ${schedulerStatus.is_running ? 'bg-yellow-500 animate-pulse' : 'bg-green-500'}`} />
							{schedulerStatus.is_running ? '运行中' : '待命中'}
						</span>
					</div>
					{schedulerStatus.scheduled && (
						<p className="text-xs text-gray-400">定时时间：{schedulerStatus.scheduled}</p>
					)}
					{schedulerStatus.next_run && (
						<p className="text-xs text-gray-400">下次执行：{schedulerStatus.next_run}</p>
					)}
					{schedulerStatus.last_run && (
						<p className="text-xs text-gray-400">上次执行：{schedulerStatus.last_run}{schedulerStatus.daily_count > 0 ? ` (推荐了 ${schedulerStatus.daily_count} 篇)` : ''}</p>
					)}
					{schedulerStatus.last_error && (
						<p className="text-xs text-red-500">上次错误：{schedulerStatus.last_error}</p>
					)}
				</div>
			)}

			<fieldset className="space-y-3">
				<legend className="text-xs font-medium text-gray-500 dark:text-gray-400">推荐 API (评分用)</legend>
				<p className="text-xs text-gray-400">用于论文评分和用户偏好分析的 LLM API。留空则使用主 API 配置。</p>
				<div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">API Key</label><input type="password" value={recommendConfig?.api.scoring?.api_key ?? ''} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, api: { ...prev.api, scoring: { ...prev.api.scoring!, api_key: e.target.value } } } : prev)} className={inputClass} /></div>
				<div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">Base URL</label><input type="text" value={recommendConfig?.api.scoring?.base_url ?? ''} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, api: { ...prev.api, scoring: { ...prev.api.scoring!, base_url: e.target.value } } } : prev)} className={inputClass} placeholder="https://api.openai.com/v1" /></div>
				<div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">Model</label><input type="text" value={recommendConfig?.api.scoring?.model ?? ''} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, api: { ...prev.api, scoring: { ...prev.api.scoring!, model: e.target.value } } } : prev)} className={inputClass} placeholder="gpt-4o" /></div>
			</fieldset>
			<hr className="border-gray-200 dark:border-gray-800" />
			<fieldset className="space-y-3">
				<legend className="text-xs font-medium text-gray-500 dark:text-gray-400">翻译 API (推荐摘要翻译用)</legend>
				<p className="text-xs text-gray-400">用于把推荐论文的标题/摘要翻译为中文。留空则不进行翻译，原始英文保留。</p>
				<div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">API Key</label><input type="password" value={recommendConfig?.api.translation?.api_key ?? ''} onChange={(e) => updateTranslation('api_key', e.target.value)} className={inputClass} /></div>
				<div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">Base URL</label><input type="text" value={recommendConfig?.api.translation?.base_url ?? ''} onChange={(e) => updateTranslation('base_url', e.target.value)} className={inputClass} placeholder="https://api.openai.com/v1" /></div>
				<div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">Model</label><input type="text" value={recommendConfig?.api.translation?.model ?? ''} onChange={(e) => updateTranslation('model', e.target.value)} className={inputClass} placeholder="gpt-4o" /></div>
			</fieldset>
			<hr className="border-gray-200 dark:border-gray-800" />
			<fieldset className="space-y-3">
				<legend className="text-xs font-medium text-gray-500 dark:text-gray-400">arXiv 订阅分类</legend>
				<div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">分类列表（用逗号分隔，如 cs.LG, cs.CV, cs.AI）</label><input type="text" value={recommendConfig?.arxiv_categories?.join(', ') ?? ''} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, arxiv_categories: e.target.value.split(',').map(s => s.trim()).filter(Boolean) } : prev)} className={inputClass} placeholder="cs.LG, cs.CV, cs.AI" /></div>
			</fieldset>
			<hr className="border-gray-200 dark:border-gray-800" />
			<fieldset className="space-y-3">
				<legend className="text-xs font-medium text-gray-500 dark:text-gray-400">推荐参数与通知</legend>
				<div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">每日推荐数量</label><input type="number" value={recommendConfig?.recommend.daily_papers ?? 20} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, recommend: { ...prev.recommend, daily_papers: parseInt(e.target.value) || 20 } } : prev)} min={1} max={100} className={inputClass} /></div>
				<div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">评分批次大小</label><input type="number" value={recommendConfig?.recommend.scoring_batch_size ?? 10} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, recommend: { ...prev.recommend, scoring_batch_size: parseInt(e.target.value) || 10 } } : prev)} min={1} max={50} className={inputClass} /><p className="text-xs text-gray-400 mt-1">每次 LLM 调用评分多少篇论文</p></div>
				<div><label className="text-xs text-gray-500 dark:text-gray-400 block mb-1">每日定时推荐时间</label><input type="time" value={recommendConfig?.recommend.scheduled_time ?? '08:00'} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, recommend: { ...prev.recommend, scheduled_time: e.target.value } } : prev)} className={inputClass} /></div>
				<div className="flex items-center gap-2">
					<input type="checkbox" id="push-to-feishu" checked={recommendConfig?.recommend.push_to_feishu ?? false} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, recommend: { ...prev.recommend, push_to_feishu: e.target.checked } } : prev)} className="w-4 h-4 rounded border-gray-300" />
					<label htmlFor="push-to-feishu" className="text-xs text-gray-500 dark:text-gray-400">推荐完成后推送飞书</label>
				</div>
			</fieldset>
		</div>
	)

	const renderPreferences = () => preferencesLoading ? <div className="flex items-center justify-center py-8"><Loader2 size={24} className="animate-spin text-gray-400" /></div> : (
		<div className="space-y-3">
			<p className="text-xs text-gray-500 dark:text-gray-400">用 Markdown 自由描述你的研究兴趣，作为推荐论文的评分依据。留空时推荐退化为按时间倒序选择未读论文。</p>
			<textarea
				value={preferencesContent}
				onChange={(e) => setPreferencesContent(e.target.value)}
				placeholder={'## 感兴趣的主题\n- ...\n\n## 偏好的研究方法/技术\n- ...\n\n## 备注\n- ...'}
				className="w-full px-3 py-2 rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 text-xs outline-none focus:ring-2 focus:ring-blue-500 font-mono resize-y"
				rows={18}
			/>
		</div>
	)

	const renderPrompts = () => promptsLoading ? <div className="flex items-center justify-center py-8"><Loader2 size={24} className="animate-spin text-gray-400" /></div> : (
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
						rows={12}
					/>
				</div>
			))}
		</div>
	)

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

				<div className="flex gap-1 px-4 py-2 bg-gray-100 dark:bg-gray-800 overflow-x-auto">
					<button onClick={() => setTab('config')} className={tabClass('config')}>API 配置</button>
					<button onClick={() => setTab('prompts')} className={tabClass('prompts')}>提示词模板</button>
					<button onClick={() => setTab('recommend')} className={tabClass('recommend')}>推荐系统</button>
					<button onClick={() => setTab('preferences')} className={tabClass('preferences')}>推荐偏好</button>
				</div>

				<div className="flex-1 overflow-y-auto p-4">
					{loading ? (
						<div className="flex items-center justify-center py-8"><Loader2 size={24} className="animate-spin text-gray-400" /></div>
					) : tab === 'config' ? renderConfig()
					: tab === 'recommend' ? renderRecommend()
					: tab === 'preferences' ? renderPreferences()
					: renderPrompts()}
				</div>

				{!loading && (
					<div className="px-4 py-3 border-t border-gray-200 dark:border-gray-800 flex justify-between gap-2">
						<p className="text-xs text-gray-400 self-center">
							{tab === 'prompts' ? '提示词保存后立即生效'
								: tab === 'recommend' ? '推荐配置保存在 ~/.config/paperagent/config.yaml'
								: tab === 'preferences' ? '偏好保存在 ~/.config/paperagent/preferences.md'
								: '配置保存在 ~/.config/paperagent/config.yaml'}
						</p>
						<div className="flex gap-2">
							<button onClick={() => close()} className="px-4 py-2 text-sm rounded-lg border border-gray-300 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800">关闭</button>
							<button
								onClick={
									tab === 'prompts' ? handleSavePrompts
									: tab === 'recommend' ? handleSaveRecommend
									: tab === 'preferences' ? handleSavePreferences
									: handleSaveConfig
								}
								disabled={
									tab === 'prompts' ? promptsSaving
									: tab === 'recommend' ? recSaving
									: tab === 'preferences' ? preferencesSaving
									: saving
								}
								className="px-4 py-2 text-sm rounded-lg bg-blue-500 hover:bg-blue-600 disabled:bg-gray-300 dark:disabled:bg-gray-700 text-white flex items-center gap-1.5"
							>
								{(tab === 'prompts' ? promptsSaving : tab === 'recommend' ? recSaving : tab === 'preferences' ? preferencesSaving : saving) && <Loader2 size={14} className="animate-spin" />}
								<Save size={14} />保存
							</button>
						</div>
					</div>
				)}
			</div>
		</div>
	)
}
