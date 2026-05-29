import { useEffect } from 'react'
import { Toaster } from 'sonner'
import { PaperList } from './components/PaperList'
import { ChatView } from './components/ChatView'
import { InputBox } from './components/InputBox'
import { ErrorBoundary } from './components/ErrorBoundary'
import { NewPaperDialog } from './components/NewPaperDialog'
import { SettingsDialog } from './components/SettingsDialog'
import { useAppStore, applyTheme } from './stores/appStore'

export default function App() {
  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const handler = () => {
      const current = useAppStore.getState().theme
      if (current === 'system') applyTheme('system')
    }
    mq.addEventListener('change', handler)
    return () => mq.removeEventListener('change', handler)
  }, [])

  return (
    <div
      className="h-screen flex flex-col"
      style={{ backgroundColor: 'var(--color-bg)', color: 'var(--color-text)' }}
    >
      <div className="flex-1 flex min-h-0">
        <PaperList />

        <div className="flex-1 flex flex-col min-w-0">
          <ErrorBoundary>
            <ChatView />
            <InputBox />
          </ErrorBoundary>
        </div>
      </div>

      <NewPaperDialog />
      <SettingsDialog />

      <Toaster
        position="top-right"
        toastOptions={{
          style: {
            fontSize: '0.875rem',
            borderRadius: '0.5rem',
            fontFamily: 'var(--font-ui)',
          },
        }}
      />
    </div>
  )
}
