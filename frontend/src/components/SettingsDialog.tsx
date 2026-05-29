import { useAppStore } from '../stores/appStore'
import { X, Sun, Moon, Monitor } from 'lucide-react'
import type { Theme } from '../types'

const themeOptions: { value: Theme; label: string; icon: typeof Sun }[] = [
  { value: 'light', label: '浅色', icon: Sun },
  { value: 'dark', label: '深色', icon: Moon },
  { value: 'system', label: '跟随系统', icon: Monitor },
]

export function SettingsDialog() {
  const { isSettingsOpen, setSettingsOpen, theme, setTheme } = useAppStore()

  if (!isSettingsOpen) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center animate-fade-in"
      style={{ backgroundColor: 'rgba(0,0,0,0.35)', backdropFilter: 'blur(2px)' }}
    >
      <div
        className="rounded-2xl shadow-lg w-full max-w-sm mx-4 overflow-hidden animate-scale-in"
        style={{
          backgroundColor: 'var(--color-surface)',
          border: '1px solid var(--color-border)',
          boxShadow: 'var(--shadow-lg)',
        }}
      >
        {/* Header */}
        <div
          className="flex items-center justify-between px-5 py-3.5"
          style={{ borderBottom: '1px solid var(--color-border-light)' }}
        >
          <h2
            className="text-sm font-semibold"
            style={{ fontFamily: 'var(--font-display)', color: 'var(--color-text)' }}
          >
            设置
          </h2>
          <button
            onClick={() => setSettingsOpen(false)}
            className="p-1.5 rounded-md hover:bg-[var(--color-bg-elevated)] transition-colors duration-150"
            style={{ color: 'var(--color-text-muted)' }}
          >
            <X size={15} />
          </button>
        </div>

        {/* Body */}
        <div className="p-5 space-y-5">
          {/* Theme */}
          <div>
            <label
              className="text-xs block mb-2.5"
              style={{ color: 'var(--color-text-muted)', fontFamily: 'var(--font-ui)' }}
            >
              外观主题
            </label>
            <div
              className="flex gap-1 rounded-lg p-0.5"
              style={{ backgroundColor: 'var(--color-bg-inset)' }}
            >
              {themeOptions.map((opt) => {
                const Icon = opt.icon
                const isActive = theme === opt.value
                return (
                  <button
                    key={opt.value}
                    onClick={() => setTheme(opt.value)}
                    className="flex-1 flex items-center justify-center gap-1.5 px-2 py-2 rounded-md text-xs transition-all duration-200"
                    style={{
                      fontFamily: 'var(--font-ui)',
                      color: isActive ? 'var(--color-accent)' : 'var(--color-text-muted)',
                      backgroundColor: isActive ? 'var(--color-surface)' : 'transparent',
                      boxShadow: isActive ? 'var(--shadow-sm)' : 'none',
                    }}
                  >
                    <Icon size={14} />
                    {opt.label}
                  </button>
                )
              })}
            </div>
          </div>

          {/* API Config Info */}
          <ConfigSection
            title="API 配置"
            body={
              <>
                API 配置在{' '}
                <code style={{ color: 'var(--color-accent)', fontFamily: 'var(--font-mono)' }}>
                  ~/.paperpaper/config.yaml
                </code>
                {' '}或通过环境变量设置。修改配置后需重启应用。
              </>
            }
          />

          {/* Export Config */}
          <ConfigSection
            title="Obsidian 导出"
            body={
              <>
                导出路径在配置文件中设置{' '}
                <code style={{ color: 'var(--color-accent)', fontFamily: 'var(--font-mono)' }}>
                  obsidian.vault_path
                </code>
                {' '}和{' '}
                <code style={{ color: 'var(--color-accent)', fontFamily: 'var(--font-mono)' }}>
                  obsidian.export_folder
                </code>
                。
              </>
            }
          />
        </div>
      </div>
    </div>
  )
}

function ConfigSection({ title, body }: { title: string; body: React.ReactNode }) {
  return (
    <div>
      <label
        className="text-xs block mb-1.5"
        style={{ color: 'var(--color-text-muted)', fontFamily: 'var(--font-ui)' }}
      >
        {title}
      </label>
      <p
        className="text-xs leading-relaxed"
        style={{ color: 'var(--color-text-secondary)', fontFamily: 'var(--font-body)' }}
      >
        {body}
      </p>
    </div>
  )
}
