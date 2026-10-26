import {
  Component,
  type ErrorInfo,
  type ReactNode,
} from 'react';
import { AlertTriangle } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Button } from '@/components/ui/button';
import { reportClientError } from '@/lib/client-error-report';
import { formatSchemaText } from '@/lib/product-schema';

export type ErrorRegion = 'chat' | 'canvas' | 'viewer_3d';

type RegionErrorBoundaryProps = {
  region: ErrorRegion;
  children: ReactNode;
};

type RegionErrorBoundaryState = {
  error: Error | null;
  attempt: number;
};

/**
 * 局部错误边界：画布、对话和 3D 查看器各包一个。出错时只替换所在区域，
 * 「重新加载此区域」递增 attempt 让子树整体重新挂载；整页边界仍在最外层兜底。
 */
export class RegionErrorBoundary extends Component<
  RegionErrorBoundaryProps,
  RegionErrorBoundaryState
> {
  state: RegionErrorBoundaryState = { error: null, attempt: 0 };

  static getDerivedStateFromError(
    error: Error,
  ): Partial<RegionErrorBoundaryState> {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(
      `[RegionErrorBoundary:${this.props.region}]`,
      error.message,
      info.componentStack,
    );
    reportClientError(`region-error-boundary:${this.props.region}`, error, { stack: info.componentStack ?? '' });
  }

  private reload = () => {
    this.setState((state) => ({ error: null, attempt: state.attempt + 1 }));
  };

  render() {
    if (this.state.error) {
      return (
        <RegionErrorFallback
          region={this.props.region}
          onReload={this.reload}
        />
      );
    }
    return (
      <RegionBody key={this.state.attempt}>{this.props.children}</RegionBody>
    );
  }
}

function RegionBody({ children }: { children: ReactNode }) {
  return <>{children}</>;
}

function RegionErrorFallback({
  region,
  onReload,
}: {
  region: ErrorRegion;
  onReload: () => void;
}) {
  const { text } = useProductSchema();
  return (
    <div
      role="alert"
      data-testid={`region-error-${region}`}
      className="flex min-h-0 min-w-0 flex-1 flex-col items-center justify-center gap-3 bg-panel p-6 text-center"
    >
      <AlertTriangle className="h-8 w-8 text-warning" />
      <h2 className="text-base font-semibold">
        {formatSchemaText(text('ui.boundary.region.title'), {
          region: text(`ui.boundary.region.${region}`),
        })}
      </h2>
      <p className="max-w-xs text-sm text-muted-foreground">
        {text('ui.boundary.region.description')}
      </p>
      <Button onClick={onReload}>{text('ui.boundary.region.retry')}</Button>
    </div>
  );
}
