import { Component, type ReactNode } from 'react'

interface Props {
  children: ReactNode
  fallback?: ReactNode
}

interface State {
  hasError: boolean
  error?: Error
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  handleReset = () => this.setState({ hasError: false, error: undefined })

  render() {
    if (this.state.hasError) {
      return this.props.fallback ?? (
        <div
          className="flex-1 flex items-center justify-center p-6 animate-fade-in-up"
          style={{ fontFamily: 'var(--font-ui)' }}
        >
          <div className="text-center">
            <p className="font-medium mb-2" style={{ color: 'var(--color-danger)' }}>
              Something went wrong
            </p>
            <p className="text-sm mb-4" style={{ color: 'var(--color-text-muted)' }}>
              {this.state.error?.message}
            </p>
            <button
              onClick={this.handleReset}
              className="px-4 py-1.5 text-sm rounded-lg transition-all duration-200 hover:scale-[1.02] active:scale-[0.98]"
              style={{
                backgroundColor: 'var(--color-accent)',
                color: '#fff',
              }}
            >
              Retry
            </button>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}
