'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import type { ComposerReference } from '@/lib/chat/composer-reference';
import { schemaText } from '@/lib/product-schema';
import { cn } from '@/lib/utils';
import { useComposerReferenceStore } from '@/store/composer-reference-store';

/**
 * The handle of a picture ("@img3") or model ("@model2") in the chat as a button: a click writes the
 * handle into the input box; a picture is also attached to the next message (or the attachment that
 * already is it is highlighted).
 */
export function HandleTag({ reference, className, wrapperClassName, testId = 'chat-result-handle' }: {
  reference: ComposerReference;
  /** Looks of the tag itself. */
  className?: string;
  /** Where the tag sits in its row (`absolute left-1 top-1`, `ml-auto`, `min-w-0`): the hover hint's wrapper carries it. */
  wrapperClassName?: string;
  testId?: string;
}) {
  const { schema } = useProductSchema();
  const refer = useComposerReferenceStore((state) => state.refer);
  const title = schemaText(schema, reference.kind === 'model' ? 'ui.studio.composer.handle_tag_model_title' : 'ui.studio.composer.handle_tag_title');
  return (
    <HintTooltip name={reference.handle} hint={title} className={wrapperClassName}>
      <button
        type="button"
        aria-label={`${title} ${reference.handle}`}
        data-testid={testId}
        data-handle={reference.handle}
        onClick={() => refer(reference)}
        className={cn('cursor-pointer rounded-sm px-1 py-0.5 text-[10px] leading-none transition-colors hover:ring-1 hover:ring-primary focus-visible:outline-2 focus-visible:outline-ring', className)}
      >
        {reference.handle}
      </button>
    </HintTooltip>
  );
}
