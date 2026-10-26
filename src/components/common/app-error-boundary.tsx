import {
  Component,
  type ErrorInfo,
  type ReactNode,
} from 'react';
import { AlertTriangle } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Button } from '@/components/ui/button';
import { reportClientError } from '@/lib/client-error-report';

type AppErrorBoundaryProps = {
  children: ReactNode;
};

type AppErrorBoundaryState = {
  error: Error | null;
};

export class AppErrorBoundary extends Component<
  AppErrorBoundaryProps,
  AppErrorBoundaryState
> {
  state: AppErrorBoundaryState = { error: null };

  static getDerivedStateFromError(
    error: Error,
  ): AppErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(
      '[AppErrorBoundary]',
      error.message,
      info.componentStack,
    );
    reportClientError('app-error-boundary', error, { stack: info.componentStack ?? '' });
  }

  private reset = () => {
    this.setState({ error: null });
  };

  render() {
    if (this.state.error) {
      return <AppErrorFallback onRetry={this.reset} />;
    }
    return this.props.children;
  }
}

function AppErrorFallback({ onRetry }: { onRetry: () => void }) {
  const { text } = useProductSchema();
  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-4 bg-background px-6 text-center">
      <AlertTriangle className="h-12 w-12 text-warning" />
      <h1 className="text-xl font-semibold">
        {text('ui.boundary.error.title')}
      </h1>
      <p className="max-w-md text-sm text-muted-foreground">
        {text('ui.boundary.error.description')}
      </p>
      <Button onClick={onRetry}>
        {text('ui.boundary.error.retry')}
      </Button>
    </div>
  );
}
