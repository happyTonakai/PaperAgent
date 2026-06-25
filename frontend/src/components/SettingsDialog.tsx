import { useState, useEffect, useRef } from 'react'
import { X, Loader2, Save, ChevronRight, ChevronsUpDown, Eye, EyeOff } from 'lucide-react'
import { useAppStore } from '../stores/appStore'
import { toast } from 'sonner'

type Tab = 'api' | 'prompts' | 'feishu' | 'recommend' | 'preferences'

interface ConfigData {
	api: { base_url: string; api_key: string; api_key_source: string; default_model: string }
	obsidian: { export_path: string }
	ui: { min_recent_rounds: number; max_input_tokens: number }
	feishu?: { enabled: boolean; app_id: string; app_secret: string; daily_recommend_chat_id: string }
}

interface ConfigForm {
	api_key: string; base_url: string; default_model: string
	min_recent_rounds: string; max_input_tokens: string; obsidian_export_path: string
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
	recommend: { enabled: boolean; daily_papers: number; scoring_batch_size: number; scheduled_time: string; push_to_feishu: boolean; diversity_ratio: number; enable_translation: boolean; excluded_keywords: string[] }
	arxiv_categories: string[]
}

const promptLabels: Record<string, string> = {
	heavy: '初始总结 (heavy)',
	light: '对话问答 (light)',
	summarize: '对话总结 (summarize)',
	scoring: '论文评分 (scoring)',
	'update-prefs': '偏好更新 (update-prefs)',
}

// ── shared style tokens ──
const inputCls =
	'w-full px-3 py-2 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] text-sm outline-none focus:ring-2 focus:ring-[var(--color-accent)] focus:border-[var(--color-accent)] transition-colors'
const labelCls = 'text-xs text-[var(--color-text-secondary)] block mb-1'
const hintCls = 'text-xs text-[var(--color-text-muted)] mt-1'
const legendCls = 'text-sm font-semibold text-[var(--color-text)]'
const dividerCls = 'border-[var(--color-border-light)]'

const tabCls = (active: boolean) =>
	`px-3 py-2 text-sm font-medium rounded-lg transition-colors ${
		active
			? 'bg-[var(--color-surface)] shadow-sm text-[var(--color-text)]'
			: 'text-[var(--color-text-secondary)] hover:text-[var(--color-text)]'
	}`

function StatusDot({ color }: { color: 'green' | 'red' | 'yellow' }) {
	return (
		<span
			className={`inline-block w-1.5 h-1.5 rounded-full ${
				color === 'green' ? 'bg-green-500' : color === 'red' ? 'bg-red-500' : 'bg-yellow-500 animate-pulse'
			}`}
		/>
	)
}

function SecretInput({
	value, onChange, placeholder, className,
}: {
	value: string
	onChange: (v: string) => void
	placeholder?: string
	className?: string
}) {
	const [show, setShow] = useState(false)
	return (
		<div className="relative">
			<input
				type={show ? 'text' : 'password'}
				value={value}
				onChange={(e) => onChange(e.target.value)}
				placeholder={placeholder}
				className={`${className ?? ''} pr-8`}
			/>
			<button
				type="button"
				onClick={() => setShow(!show)}
				className="absolute right-2 top-1/2 -translate-y-1/2 text-[var(--color-text-muted)] hover:text-[var(--color-text)] transition-colors"
				tabIndex={-1}
				aria-label={show ? '隐藏' : '显示'}
			>
				{show ? <EyeOff size={14} /> : <Eye size={14} />}
			</button>
		</div>
	)
}

// ── hook: propagate wheel from a scrollable child to its scrollable
//    ancestor when the child is at the top/bottom edge. Solves the
//    "I can only scroll the dialog from the edge" UX problem.
function usePropagateWheel<T extends HTMLElement>(): React.RefObject<T | null> {
	const ref = useRef<T | null>(null)
	useEffect(() => {
		const el = ref.current
		if (!el) return
		const handler = (e: WheelEvent) => {
			const { scrollTop, scrollHeight, clientHeight } = el
			const atTop = scrollTop <= 0
			const atBottom = scrollTop + clientHeight >= scrollHeight
			const scrollingUp = e.deltaY < 0
			const scrollingDown = e.deltaY > 0
			if ((scrollingUp && atTop) || (scrollingDown && atBottom)) {
				let parent: HTMLElement | null = el.parentElement
				while (parent) {
					const overflow = getComputedStyle(parent).overflowY
					if (overflow === 'auto' || overflow === 'scroll' || overflow === 'overlay') {
						const before = parent.scrollTop
						parent.scrollTop += e.deltaY
						if (parent.scrollTop !== before) {
							e.preventDefault()
							return
						}
					}
					parent = parent.parentElement
				}
			}
		}
		el.addEventListener('wheel', handler, { passive: false })
		return () => el.removeEventListener('wheel', handler)
	}, [])
	return ref
}


export function SettingsDialog() {
	const { isSettingsOpen, setSettingsOpen } = useAppStore()
	const [tab, setTab] = useState<Tab>('api')
	const [visible, setVisible] = useState(false)
	const [closing, setClosing] = useState(false)

	useEffect(() => {
		if (isSettingsOpen) {
			setVisible(true)
			setClosing(false)
		} else if (visible && !closing) {
			setClosing(true)
		}
	}, [isSettingsOpen, visible, closing])

	useEffect(() => {
		if (!closing) return
		const timer = setTimeout(() => setVisible(false), 200)
		return () => clearTimeout(timer)
	}, [closing])

	const pointerDownRef = useRef<EventTarget | null>(null)
	const close = () => setSettingsOpen(false)

	// ── Config (main API + UI + Obsidian + Feishu) ──
	const [config, setConfig] = useState<ConfigData | null>(null)
	const [loading, setLoading] = useState(false)
	const [saving, setSaving] = useState(false)
	const [form, setForm] = useState<ConfigForm>({ api_key: '', base_url: '', default_model: '', min_recent_rounds: '2', max_input_tokens: '30000', obsidian_export_path: '', feishu_enabled: false, feishu_app_id: '', feishu_app_secret: '', feishu_daily_recommend_chat_id: '' })
	const [apiKeyDirty, setApiKeyDirty] = useState(false)

	// ── Prompts ──
	const [prompts, setPrompts] = useState<PromptInfo[]>([])
	const [promptEdits, setPromptEdits] = useState<Record<string, string>>({})
	const [promptsLoading, setPromptsLoading] = useState(false)
	const [promptsSaving, setPromptsSaving] = useState(false)

	// ── Feishu status ──
	const [feishuStatus, setFeishuStatus] = useState<{ connected: boolean; enabled: boolean; last_error?: string } | null>(null)

	// ── Recommend settings (scoring API, translation API, recommend params, arxiv) ──
	const [recommendConfig, setRecommendConfig] = useState<RecommendConfigData | null>(null)
	const [recLoading, setRecLoading] = useState(false)
	const [recSaving, setRecSaving] = useState(false)


	// ── Prompts accordion state ──
	const [expandedPrompts, setExpandedPrompts] = useState<Record<string, boolean>>({})
	const togglePrompt = (name: string) => setExpandedPrompts((prev) => ({ ...prev, [name]: !prev[name] }))
	const expandAllPrompts = () => {
		const all: Record<string, boolean> = {}
		prompts.forEach((p) => { all[p.name] = true })
		setExpandedPrompts(all)
	}
	const collapseAllPrompts = () => setExpandedPrompts({})

	// ── Excluded keywords raw input (comma-separated) ──
	const [excludedKeywordsInput, setExcludedKeywordsInput] = useState('')
	useEffect(() => {
		if (recommendConfig) {
			setExcludedKeywordsInput(recommendConfig.recommend.excluded_keywords?.join(', ') ?? '')
		}
	}, [recommendConfig])

	// ── Preferences ──
	const [preferencesContent, setPreferencesContent] = useState('')
	const [preferencesOriginal, setPreferencesOriginal] = useState('')
	const [preferencesLoading, setPreferencesLoading] = useState(false)
	const [preferencesSaving, setPreferencesSaving] = useState(false)
	const preferencesTaRef = usePropagateWheel<HTMLTextAreaElement>()
	const promptsTaRef = usePropagateWheel<HTMLTextAreaElement>()

	// ── Scheduler status ──
	const [schedulerStatus, setSchedulerStatus] = useState<SchedulerStatus | null>(null)

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
				setForm({ api_key: '', base_url: data.api.base_url, default_model: data.api.default_model, min_recent_rounds: String(data.ui.min_recent_rounds), max_input_tokens: String(data.ui.max_input_tokens), obsidian_export_path: data.obsidian.export_path, feishu_enabled: data.feishu?.enabled ?? false, feishu_app_id: '', feishu_app_secret: '', feishu_daily_recommend_chat_id: data.feishu?.daily_recommend_chat_id ?? '' })
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

		fetch('/api/feishu/status')
			.then((r) => r.json())
			.then((data) => setFeishuStatus(data))
			.catch(() => {})

		setRecLoading(true)
		fetch('/api/recommend/config')
			.then((r) => r.json())
			.then((data: RecommendConfigData) => {
				setRecommendConfig(data)
			})
			.catch(() => {})
			.finally(() => setRecLoading(false))

		setPreferencesLoading(true)
		fetch('/api/recommend/preferences')
			.then((r) => r.json())
			.then((data: PreferencesData) => {
				const c = data.content ?? ''
				setPreferencesContent(c)
				setPreferencesOriginal(c)
			})
			.catch(() => {})
			.finally(() => setPreferencesLoading(false))

		fetch('/api/recommend/scheduler-status')
			.then((r) => r.json())
			.then(setSchedulerStatus)
			.catch(() => {})
	}, [isSettingsOpen])

	if (!visible) return null

	// ── Save handlers ──

	const handleSaveApi = async () => {
		setSaving(true)
		try {
			// 1) Save main config (API + UI + Obsidian)
			const body: Record<string, unknown> = {}
			if (apiKeyDirty && form.api_key.trim()) body['api_key'] = form.api_key.trim()
			if (form.base_url !== config?.api.base_url) body['base_url'] = form.base_url
			if (form.default_model !== config?.api.default_model) body['default_model'] = form.default_model
			if (String(form.min_recent_rounds) !== String(config?.ui.min_recent_rounds)) body['min_recent_rounds'] = Number(form.min_recent_rounds)
			if (String(form.max_input_tokens) !== String(config?.ui.max_input_tokens)) body['max_input_tokens'] = Number(form.max_input_tokens)
			if (form.obsidian_export_path !== config?.obsidian.export_path) body['obsidian_export_path'] = form.obsidian_export_path

			let mainChanged = Object.keys(body).length > 0
			if (!mainChanged) { toast('没有需要保存的更改'); setSaving(false); close(); return }

			const res = await fetch('/api/config', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
			if (!res.ok) throw new Error((await res.json().catch(() => ({ error: '保存失败' })) as { error?: string }).error)
			setApiKeyDirty(false)
			if (body.api_key) setForm((f) => ({ ...f, api_key: '' }))
			toast.success('配置已保存')
			close()
		} catch (err) { toast.error('保存失败: ' + (err instanceof Error ? err.message : '未知错误')) }
		finally { setSaving(false) }
	}

	const handleSaveFeishu = async () => {
		setSaving(true)
		const body: Record<string, unknown> = {}
		if (form.feishu_enabled !== (config?.feishu?.enabled ?? false)) body['feishu_enabled'] = form.feishu_enabled
		if (form.feishu_app_id && form.feishu_app_id !== '••••••' && form.feishu_app_id !== config?.feishu?.app_id) body['feishu_app_id'] = form.feishu_app_id
		if (form.feishu_app_secret && form.feishu_app_secret !== '••••••' && form.feishu_app_secret !== config?.feishu?.app_secret) body['feishu_app_secret'] = form.feishu_app_secret
		if (form.feishu_daily_recommend_chat_id !== (config?.feishu?.daily_recommend_chat_id ?? '')) body['feishu_daily_recommend_chat_id'] = form.feishu_daily_recommend_chat_id
		if (Object.keys(body).length === 0) { toast('没有需要保存的更改'); setSaving(false); close(); return }
		try {
			const res = await fetch('/api/config', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
			if (!res.ok) throw new Error((await res.json().catch(() => ({ error: '保存失败' })) as { error?: string }).error)
			toast.success('飞书配置已保存')
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
			const body: Record<string, unknown> = {
				recommend: recommendConfig.recommend,
				arxiv_categories: recommendConfig.arxiv_categories,
			}
			const res = await fetch('/api/recommend/config', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
			if (!res.ok) throw new Error((await res.json().catch(() => ({ error: '保存失败' })) as { error?: string }).error)
			toast.success('推荐配置已保存')
			fetch('/api/recommend/scheduler-status')
				.then((r) => r.json())
				.then(setSchedulerStatus)
				.catch(() => {})
			close()
		} catch (err) { toast.error('保存失败: ' + (err instanceof Error ? err.message : '未知错误')) }
		finally { setRecSaving(false) }
	}

	const handleSavePreferences = async () => {
		const prefsChanged = preferencesContent !== preferencesOriginal
		if (!prefsChanged && !recommendConfig) {
			toast('没有需要保存的更改')
			close()
			return
		}
		setPreferencesSaving(true)
		try {
			// Always persist excluded keywords (and other recommend config) — even if prefs didn't change
			if (recommendConfig) {
				// Parse the raw comma-separated input into an array
				const parsedKeywords = excludedKeywordsInput.split(',').map(s => s.trim()).filter(Boolean)
				const cfgRes = await fetch('/api/recommend/config', {
					method: 'PUT',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({
						recommend: { ...recommendConfig.recommend, excluded_keywords: parsedKeywords },
						arxiv_categories: recommendConfig.arxiv_categories,
					}),
				})
				if (!cfgRes.ok) throw new Error('排除关键词保存失败')
			}
			// Save preferences if changed
			if (prefsChanged) {
				const res = await fetch('/api/recommend/preferences', {
					method: 'PUT',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ content: preferencesContent }),
				})
				if (!res.ok) throw new Error((await res.json().catch(() => ({ error: '保存失败' })) as { error?: string }).error)
				setPreferencesOriginal(preferencesContent)
			}
			toast.success('偏好已保存')
			close()
		} catch (err) { toast.error('保存失败: ' + (err instanceof Error ? err.message : '未知错误')) }
		finally { setPreferencesSaving(false) }
	}

	const updateForm = (key: keyof ConfigForm, value: string) => { setForm((f) => ({ ...f, [key]: value })); if (key === 'api_key') setApiKeyDirty(true) }

	// ── Panel renders ──

	const renderApi = () => (
		<div className="space-y-4">
			{/* 1. 主 API — 论文对话系统 */}
			<fieldset className="space-y-3">
				<legend className={legendCls}>主 API · 论文对话</legend>
				<p className={hintCls}>论文解析、摘要生成、多轮问答使用的 LLM API。</p>
				<div>
					<label className={labelCls}>
						API Key{config && <span className="ml-1 text-[var(--color-text-muted)]">({config.api.api_key_source === 'config' ? '文件配置' : '环境变量'}: {config.api.api_key})</span>}
					</label>
					<SecretInput value={form.api_key} onChange={(v) => updateForm('api_key', v)} placeholder={config ? '输入新密钥以替换...' : ''} className={inputCls} />
					{!apiKeyDirty && <p className={hintCls}>输入新密钥以替换。全大写名称（如 OPENAI_API_KEY）则引用环境变量</p>}
				</div>
				<div><label className={labelCls}>Base URL</label><input type="text" value={form.base_url} onChange={(e) => updateForm('base_url', e.target.value)} className={inputCls} /></div>
				<div><label className={labelCls}>Default Model</label><input type="text" value={form.default_model} onChange={(e) => updateForm('default_model', e.target.value)} className={inputCls} /></div>
			</fieldset>

			<hr className={dividerCls} />

	

			{/* 4. 上下文控制 */}
			<fieldset className="space-y-3">
				<legend className={legendCls}>上下文控制</legend>
				<div><label className={labelCls}>最小保留轮数</label><input type="number" value={form.min_recent_rounds} onChange={(e) => updateForm('min_recent_rounds', e.target.value)} min={1} max={50} className={inputCls} /><p className={hintCls}>当输入 token 接近上限时，至少保留此轮数的最近上下文</p></div>
				<div><label className={labelCls}>最大输入 Token</label><input type="number" value={form.max_input_tokens} onChange={(e) => updateForm('max_input_tokens', e.target.value)} min={1000} max={200000} step={1000} className={inputCls} /><p className={hintCls}>输入超过此值时自动截断上下文到最小轮数（默认 30000）</p></div>
			</fieldset>

			<hr className={dividerCls} />

			{/* 5. 导出设置 */}
			<fieldset className="space-y-3">
				<legend className={legendCls}>导出设置</legend>
				<p className={hintCls}>将论文和对话导出为 Markdown 文件，保存到此文件夹。支持 ~ 路径展开。</p>
				<div><label className={labelCls}>导出文件夹</label><input type="text" value={form.obsidian_export_path} onChange={(e) => updateForm('obsidian_export_path', e.target.value)} placeholder="~/Papers" className={inputCls} /></div>
			</fieldset>
		</div>
	)

	const renderFeishu = () => (
		<div className="space-y-4">
			<fieldset className="space-y-3">
				<legend className={legendCls}>飞书 Bot 配置</legend>
				<p className={hintCls}>启用后在飞书中与论文对话，或推送每日推荐。需在飞书开放平台开启机器人能力并订阅「消息和群组」事件。</p>
				<div className="flex items-center gap-2">
					<input type="checkbox" id="feishu-enabled" checked={form.feishu_enabled} onChange={(e) => setForm((f) => ({ ...f, feishu_enabled: e.target.checked }))} className="w-4 h-4 rounded border-[var(--color-border)]" />
					<label htmlFor="feishu-enabled" className={labelCls}>启用飞书 Bot</label>
					{feishuStatus && feishuStatus.enabled && (
						<span className={`inline-flex items-center gap-1 text-xs px-1.5 py-0.5 rounded ${
							feishuStatus.connected
								? 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
								: 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
						}`}>
							<StatusDot color={feishuStatus.connected ? 'green' : 'red'} />
							{feishuStatus.connected ? '已连接' : '未连接'}
						</span>
					)}
				</div>
				{feishuStatus && feishuStatus.last_error && (<p className="text-xs text-red-500">错误: {feishuStatus.last_error}</p>)}
				<div><label className={labelCls}>App ID {config?.feishu?.app_id && <span className="ml-1 text-[var(--color-text-muted)]">(当前: {config.feishu.app_id})</span>}</label><input type="text" value={form.feishu_app_id} onChange={(e) => updateForm('feishu_app_id', e.target.value)} placeholder="cli_xxxxx" className={inputCls} /></div>
				<div><label className={labelCls}>App Secret {config?.feishu?.app_secret && <span className="ml-1 text-[var(--color-text-muted)]">(当前: {config.feishu.app_secret})</span>}</label><SecretInput value={form.feishu_app_secret} onChange={(v) => updateForm('feishu_app_secret', v)} placeholder={config?.feishu?.app_secret ? '输入新 Secret 以替换...' : '飞书应用 Secret'} className={inputCls} /></div>
				<div><label className={labelCls}>每日推荐 Chat ID {config?.feishu?.daily_recommend_chat_id && <span className="ml-1 text-[var(--color-text-muted)]">(当前: {config.feishu.daily_recommend_chat_id})</span>}</label><input type="text" value={form.feishu_daily_recommend_chat_id} onChange={(e) => updateForm('feishu_daily_recommend_chat_id', e.target.value)} placeholder="oc_xxxxxxxxx" className={inputCls} /></div>
				<p className={hintCls}>保存后自动重连飞书 WebSocket。</p>
			</fieldset>
		</div>
	)

	const renderRecommend = () => recLoading ? (
		<div className="flex items-center justify-center py-8"><Loader2 size={24} className="animate-spin text-[var(--color-text-muted)]" /></div>
	) : (
		<div className="space-y-4">
			<fieldset className="space-y-3">
				<legend className={legendCls}>总开关</legend>
				<p className={hintCls}>关闭后，RSS 抓取与每日推荐管线完全跳过；后台 scheduler 不会再醒来。配置项仍保留，下次开启即可恢复。</p>
				<div className="flex items-center gap-2">
					<input
						type="checkbox"
						id="recommend-enabled"
						checked={recommendConfig?.recommend.enabled ?? false}
						onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, recommend: { ...prev.recommend, enabled: e.target.checked } } : prev)}
						className="w-4 h-4 rounded border-[var(--color-border)]"
					/>
					<label htmlFor="recommend-enabled" className={labelCls}>启用每日推荐管线</label>
				</div>
			</fieldset>
			<hr className={dividerCls} />
			<fieldset className="space-y-3">
				<legend className={legendCls}>arXiv 订阅分类</legend>
				<p className={hintCls}>每天从这些 arXiv 分类中拉取最新论文。</p>
				<div><label className={labelCls}>分类列表（用逗号分隔，如 cs.LG, cs.CV, cs.AI）</label><input type="text" value={recommendConfig?.arxiv_categories?.join(', ') ?? ''} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, arxiv_categories: e.target.value.split(',').map(s => s.trim()).filter(Boolean) } : prev)} className={inputCls} placeholder="cs.LG, cs.CV, cs.AI" /></div>
				{(recommendConfig?.recommend.enabled ?? false) && (recommendConfig?.arxiv_categories?.length ?? 0) === 0 && (
					<p className="text-xs text-amber-600 dark:text-amber-400">启用推荐时必须至少填写一个分类，否则后端会拒绝保存。</p>
				)}
			</fieldset>
			<hr className={dividerCls} />
			<fieldset className="space-y-3">
				<legend className={legendCls}>推荐参数</legend>
				<div><label className={labelCls}>每日推荐数量</label><input type="number" value={recommendConfig?.recommend.daily_papers ?? 20} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, recommend: { ...prev.recommend, daily_papers: parseInt(e.target.value) || 20 } } : prev)} min={1} max={100} className={inputCls} /></div>
				<div><label className={labelCls}>评分批次大小</label><input type="number" value={recommendConfig?.recommend.scoring_batch_size ?? 10} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, recommend: { ...prev.recommend, scoring_batch_size: parseInt(e.target.value) || 10 } } : prev)} min={1} max={50} className={inputCls} /><p className={hintCls}>每次 LLM 调用评分多少篇论文</p></div>
				<div><label className={labelCls}>探索比例 (diversity_ratio)</label><input type="number" value={recommendConfig?.recommend.diversity_ratio ?? 0.3} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, recommend: { ...prev.recommend, diversity_ratio: parseFloat(e.target.value) || 0 } } : prev)} min={0} max={1} step={0.05} className={inputCls} /><p className={hintCls}>0 = 纯按评分排序，1 = 完全随机探索。推荐值 0.2–0.3</p></div>
				<div><label className={labelCls}>每日定时推荐时间</label><input type="time" value={recommendConfig?.recommend.scheduled_time ?? '08:00'} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, recommend: { ...prev.recommend, scheduled_time: e.target.value } } : prev)} className={inputCls} /></div>
				{/* Scheduler status — placed right after the time input, before the feishu push toggle */}
				{schedulerStatus && (
					<div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-bg)] p-3 space-y-1.5">
						<div className="text-xs font-medium text-[var(--color-text-secondary)] mb-1">⏱ 定时调度状态</div>
						<div className="flex items-center gap-2 text-xs">
							<span className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded ${
								schedulerStatus.is_running
									? 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-300'
									: 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
							}`}>
								<StatusDot color={schedulerStatus.is_running ? 'yellow' : 'green'} />
								{schedulerStatus.is_running ? '运行中' : '待命中'}
							</span>
						</div>
						{schedulerStatus.scheduled && <p className={hintCls}>定时时间：{schedulerStatus.scheduled}</p>}
						{schedulerStatus.next_run && <p className={hintCls}>下次执行：{schedulerStatus.next_run}</p>}
						{schedulerStatus.last_run && <p className={hintCls}>上次执行：{schedulerStatus.last_run}{schedulerStatus.daily_count > 0 ? ` (推荐了 ${schedulerStatus.daily_count} 篇)` : ''}</p>}
						{schedulerStatus.last_error && <p className="text-xs text-red-500">上次错误：{schedulerStatus.last_error}</p>}
					</div>
				)}
				<div className="flex items-center gap-2">
					<input type="checkbox" id="push-to-feishu" checked={recommendConfig?.recommend.push_to_feishu ?? false} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, recommend: { ...prev.recommend, push_to_feishu: e.target.checked } } : prev)} className="w-4 h-4 rounded border-[var(--color-border)]" />
					<label htmlFor="push-to-feishu" className={labelCls}>推荐完成后推送飞书</label>
				</div>
				<div className="flex items-center gap-2">
					<input type="checkbox" id="enable-translation" checked={recommendConfig?.recommend.enable_translation ?? false} onChange={(e) => setRecommendConfig(prev => prev ? { ...prev, recommend: { ...prev.recommend, enable_translation: e.target.checked } } : prev)} className="w-4 h-4 rounded border-[var(--color-border)]" />
					<label htmlFor="enable-translation" className={labelCls}>翻译推荐论文标题/摘要（使用主 API）</label>
				</div>
			</fieldset>
		</div>
	)

	const renderPreferences = () => preferencesLoading ? (
		<div className="flex items-center justify-center py-8"><Loader2 size={24} className="animate-spin text-[var(--color-text-muted)]" /></div>
	) : (
		<div className="space-y-3">
			<p className={hintCls}>用 Markdown 自由描述你的研究兴趣，作为推荐论文的评分依据。留空时推荐退化为按时间倒序选择未读论文。</p>
			<textarea
				ref={preferencesTaRef}
				value={preferencesContent}
				onChange={(e) => setPreferencesContent(e.target.value)}
				placeholder={'## 感兴趣的主题\n- ...\n\n## 偏好的研究方法/技术\n- ...\n\n## 备注\n- ...'}
				className={`${inputCls} font-mono resize-y`}
				rows={18}
			/>
			<hr className={dividerCls} />
			<label className={labelCls}>排除关键词（预过滤）</label>
			<p className={hintCls}>RSS 抓取后对标题和摘要做不区分大小写的子串匹配，命中直接丢弃不入库。多个关键词用逗号分隔。</p>
			<textarea
				value={excludedKeywordsInput}
				onChange={(e) => setExcludedKeywordsInput(e.target.value)}
				className={`${inputCls} font-mono resize-y`}
				rows={4}
				placeholder={'federated learning, blockchain, knowledge distillation'}
			/>
		</div>
	)

	const renderPrompts = () => promptsLoading ? (
		<div className="flex items-center justify-center py-8"><Loader2 size={24} className="animate-spin text-[var(--color-text-muted)]" /></div>
	) : (
		<div className="space-y-1">
			{/* toolbar */}
			<div className="flex items-center gap-2 mb-3">
				<button
					onClick={expandAllPrompts}
					className="text-xs px-2 py-1 rounded border border-[var(--color-border)] text-[var(--color-text-secondary)] hover:bg-[var(--color-bg-elevated)] transition-colors flex items-center gap-1"
				>
					<ChevronsUpDown size={12} />
					全部展开
				</button>
				<button
					onClick={collapseAllPrompts}
					className="text-xs px-2 py-1 rounded border border-[var(--color-border)] text-[var(--color-text-secondary)] hover:bg-[var(--color-bg-elevated)] transition-colors"
				>
					全部收起
				</button>
			</div>

			{prompts.map((p) => {
				const isOpen = expandedPrompts[p.name] ?? false
				return (
					<div key={p.name} className="rounded-lg border border-[var(--color-border)] overflow-hidden">
						<button
							onClick={() => togglePrompt(p.name)}
							className="w-full flex items-center gap-2 px-3 py-2.5 text-left hover:bg-[var(--color-bg)] transition-colors"
						>
							<ChevronRight
								size={14}
								className={`text-[var(--color-text-muted)] transition-transform duration-200 flex-shrink-0 ${isOpen ? 'rotate-90' : ''}`}
							/>
							<span className="text-xs font-medium text-[var(--color-text)]">{promptLabels[p.name] || p.name}</span>
							{p.source === 'custom' && <span className="text-xs text-[var(--color-accent)]">已自定义</span>}
						</button>
						{/* Animated accordion body */}
						<div className={`accordion-item ${isOpen ? 'open' : ''}`}>
							<div className="accordion-content">
								<div className="px-3 pb-3">
									<textarea
										ref={promptsTaRef}
										value={promptEdits[p.name] || ''}
										onChange={(e) => setPromptEdits((prev) => ({ ...prev, [p.name]: e.target.value }))}
										className={`${inputCls} font-mono resize-y`}
										rows={14}
									/>
								</div>
							</div>
						</div>
					</div>
				)
			})}
		</div>
	)

	const footerHint = () => {
		switch (tab) {
			case 'api': return '配置文件位置: ~/.config/paperagent/config.yaml'
			case 'prompts': return '提示词保存后立即生效'
			case 'feishu': return '飞书配置保存在 ~/.config/paperagent/config.yaml'
			case 'recommend': return '推荐配置保存在 ~/.config/paperagent/config.yaml'
			case 'preferences': return '偏好保存在 ~/.config/paperagent/preferences.md'
		}
	}

	const saveHandler = () => {
		switch (tab) {
			case 'api': return handleSaveApi
			case 'prompts': return handleSavePrompts
			case 'feishu': return handleSaveFeishu
			case 'recommend': return handleSaveRecommend
			case 'preferences': return handleSavePreferences
		}
	}

	const isTabSaving = () => {
		switch (tab) {
			case 'api': case 'feishu': return saving
			case 'prompts': return promptsSaving
			case 'recommend': return recSaving
			case 'preferences': return preferencesSaving
		}
	}

	// Display status bar between header and tabs when there's a feishu issue
	const hasStatusBar = feishuStatus?.enabled && (!feishuStatus.connected || feishuStatus.last_error)

	return (
		<div
			className={`fixed inset-0 z-50 flex items-center justify-center bg-black/40 ${closing ? 'animate-fade-out' : 'animate-fade-in'}`}
			onPointerDown={(e) => { pointerDownRef.current = e.target }}
			onClick={(e) => { if (e.target === e.currentTarget && pointerDownRef.current === e.currentTarget) close() }}
		>
			<div
				className={`bg-[var(--color-surface)] rounded-xl shadow-2xl w-full max-w-lg mx-4 max-h-[90vh] overflow-hidden flex flex-col ${closing ? 'animate-scale-out' : 'animate-scale-in'}`}
				style={{ fontFamily: 'var(--font-ui)' }}
			>
				{/* Header */}
				<div className="flex items-center justify-between px-4 py-3 border-b border-[var(--color-border)]">
					<h2 className="text-sm font-semibold text-[var(--color-text)]" style={{ fontFamily: 'var(--font-display)' }}>设置</h2>
					<button onClick={() => close()} className="p-1 rounded hover:bg-[var(--color-bg-elevated)] transition-colors text-[var(--color-text-muted)]"><X size={16} /></button>
				</div>

				{/* Status bar — only when there's something to report */}
				{hasStatusBar && (
					<div className="px-4 py-1.5 bg-[var(--color-bg)] border-b border-[var(--color-border-light)] space-y-0.5 text-xs">
						{/* Feishu — only when enabled but not connected */}
						{feishuStatus?.enabled && (!feishuStatus.connected || feishuStatus.last_error) && (
							<div className="flex items-center gap-1.5">
								<StatusDot color="red" />
								<span className="text-red-500">飞书未连接</span>
								{feishuStatus.last_error && <span className="text-red-400">· {feishuStatus.last_error}</span>}
							</div>
						)}
					</div>
				)}

				{/* Tab bar */}
				<div className="flex gap-1 px-4 py-2 bg-[var(--color-bg-elevated)] overflow-x-auto border-b border-[var(--color-border-light)]">
					<button onClick={() => setTab('api')} className={tabCls(tab === 'api')}>API 与凭据</button>
					<button onClick={() => setTab('prompts')} className={tabCls(tab === 'prompts')}>提示词模板</button>
					<button onClick={() => setTab('feishu')} className={tabCls(tab === 'feishu')}>飞书机器人</button>
					<button onClick={() => setTab('recommend')} className={tabCls(tab === 'recommend')}>推荐系统</button>
					<button onClick={() => setTab('preferences')} className={tabCls(tab === 'preferences')}>推荐偏好</button>
				</div>

				{/* Content */}
				<div className="flex-1 overflow-y-auto p-4">
					{loading ? (
						<div className="flex items-center justify-center py-8"><Loader2 size={24} className="animate-spin text-[var(--color-text-muted)]" /></div>
					) : tab === 'api' ? renderApi()
					: tab === 'prompts' ? renderPrompts()
					: tab === 'feishu' ? renderFeishu()
					: tab === 'recommend' ? renderRecommend()
					: renderPreferences()}
				</div>

				{/* Footer */}
				{!loading && (
					<div className="px-4 py-3 border-t border-[var(--color-border)] flex justify-between gap-2">
						<p className="text-xs text-[var(--color-text-muted)] self-center">{footerHint()}</p>
						<div className="flex gap-2">
							<button onClick={() => close()} className="px-4 py-2 text-sm rounded-lg border border-[var(--color-border)] text-[var(--color-text-secondary)] hover:bg-[var(--color-bg-elevated)] transition-colors">
								关闭
							</button>
							<button
								onClick={saveHandler()}
								disabled={isTabSaving()}
								className="px-4 py-2 text-sm rounded-lg bg-[var(--color-accent)] hover:bg-[var(--color-accent-hover)] disabled:opacity-50 disabled:bg-[var(--color-text-muted)] text-white flex items-center gap-1.5 transition-colors"
							>
								{isTabSaving() && <Loader2 size={14} className="animate-spin" />}
								<Save size={14} />保存
							</button>
						</div>
					</div>
				)}
			</div>
		</div>
	)
}
