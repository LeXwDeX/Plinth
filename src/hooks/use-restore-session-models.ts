import { useEffect, useRef } from 'react';
import useSWR from 'swr';

import { useProductSchema } from '@/components/product-schema-provider';
import { useWorkspaceOfferings } from '@/hooks/use-workspace-offerings';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { restorableSessionModels } from '@/lib/workspace/session-models';
import { useChatStore } from '@/store/chat-store';
import { useWorkspaceStore } from '@/store/workspace-store';

/**
 * #318: when a session opens in a browser that has no model picked for a
 * domain, take the model last sent in that session (from the session read),
 * if it is still offered. Local picks win; each session is restored once.
 */
export function useRestoreSessionModels(): void {
  const { schema } = useProductSchema();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const modelByDomain = useWorkspaceStore((state) => state.modelByDomain);
  const setModel = useWorkspaceStore((state) => state.setModel);
  const switchRetiredModel = useWorkspaceStore((state) => state.switchRetiredModel);
  const image = useWorkspaceOfferings(schema, 'image');
  const model3d = useWorkspaceOfferings(schema, 'model-3d');
  const restoredFor = useRef<string | null>(null);
  const missing = modelByDomain.image === null || modelByDomain['model-3d'] === null;
  const wanted = activeSessionId !== null && missing && restoredFor.current !== activeSessionId;
  const { data: session } = useSWR(
    wanted && schema.chat.api.response_fields.generation_models ? ['session-models', schema.revision, activeSessionId] : null,
    ([, , sessionId]: readonly [string, string, string]) => createChatBrowserClient(schema).getSession(sessionId),
    { revalidateOnFocus: false, shouldRetryOnError: false },
  );
  const choicesReady = !image.isLoading && !model3d.isLoading;
  useEffect(() => {
    if (!session || session.id !== activeSessionId || restoredFor.current === activeSessionId || !choicesReady) return;
    restoredFor.current = activeSessionId;
    const retired = schema.runtime.frontend.retired_generation_models;
    const restored = restorableSessionModels(
      useWorkspaceStore.getState().modelByDomain,
      session.generationModels,
      {
        image: image.error ? undefined : image.modelChoices.map((choice) => choice.model_id),
        'model-3d': model3d.error ? undefined : model3d.modelChoices.map((choice) => choice.model_id),
      },
      retired,
    );
    for (const [domain, model] of Object.entries(restored) as [keyof typeof restored, string][]) {
      // The server reports a retired model as its successor and names the retired one (#329 follow-up); an older
      // server reports the retired model itself.
      const recorded = session.generationModelsRetired?.[domain] ?? session.generationModels?.[domain];
      const entry = recorded !== model ? retired?.find((item) => item.model_id === recorded) : undefined;
      const choices = domain === 'image' ? image.modelChoices : model3d.modelChoices;
      if (entry) {
        // #329: the session used a retired model; its successor is restored and the composer says so.
        switchRetiredModel(domain, model, { from: entry.model_name, to: choices.find((choice) => choice.model_id === model)?.model_name ?? model });
      } else {
        setModel(domain, model);
      }
    }
  }, [schema, session, activeSessionId, choicesReady, image.error, image.modelChoices, model3d.error, model3d.modelChoices, setModel, switchRetiredModel]);
}
