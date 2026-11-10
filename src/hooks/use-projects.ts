'use client';

import { useCallback, useEffect, useMemo } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { createProjectBrowserClient } from '@/lib/projects/browser-client';
import type { ProjectSummary } from '@/lib/projects/browser-client';
import { useChatStore } from '@/store/chat-store';
import { useProjectStore } from '@/store/project-store';

/**
 * #240: loads the owner's projects once, resolves the open session's project
 * and assigns the selected project to that session. A new session joins the
 * last selected project.
 */
export function useProjects(sessionId: string | null) {
  const { schema } = useProductSchema();
  const client = useMemo(() => createProjectBrowserClient(schema), [schema]);
  const chatClient = useMemo(() => createChatBrowserClient(schema), [schema]);
  const projects = useProjectStore((state) => state.projects);
  const loaded = useProjectStore((state) => state.loaded);
  const loading = useProjectStore((state) => state.loading);
  const failed = useProjectStore((state) => state.failed);
  const activeProjectId = useProjectStore((state) => state.activeProjectId);
  const setProjects = useProjectStore((state) => state.setProjects);
  const setActiveProjectId = useProjectStore((state) => state.setActiveProjectId);
  const upsertProject = useProjectStore((state) => state.upsertProject);
  const removeProject = useProjectStore((state) => state.removeProject);
  const setStatus = useProjectStore((state) => state.setStatus);
  const session = useChatStore((state) => (sessionId ? state.sessions?.find((item) => item.id === sessionId) : undefined));
  const sessionProjectId = session?.projectId ?? '';

  const refresh = useCallback(async (signal?: AbortSignal) => {
    if (loading) return;
    setStatus({ loading: true, failed: false });
    try {
      setProjects(await client.listProjects(signal));
    } catch {
      if (!signal?.aborted) setStatus({ loading: false, failed: true });
    }
  }, [client, loading, setProjects, setStatus]);

  useEffect(() => {
    if (loaded || loading) return;
    const controller = new AbortController();
    void refresh(controller.signal);
    return () => controller.abort();
  }, [loaded, loading, refresh]);

  // The open session decides the shown project; the store keeps the choice for
  // the next new session.
  useEffect(() => {
    if (!sessionId) return;
    setActiveProjectId(sessionProjectId || null);
  }, [sessionId, sessionProjectId, setActiveProjectId]);

  const select = useCallback(async (projectId: string | null) => {
    if (!sessionId) {
      setActiveProjectId(projectId);
      return;
    }
    const next = projectId ?? '';
    const updated = await chatClient.setSessionProject(sessionId, next);
    useChatStore.getState().updateSessionProject(sessionId, {
      projectId: updated.projectId,
      styleLockSource: updated.styleLockSource,
    });
    setActiveProjectId(updated.projectId || null);
  }, [chatClient, sessionId, setActiveProjectId]);

  const create = useCallback(async (name: string): Promise<ProjectSummary> => {
    const created = await client.createProject(name);
    upsertProject(created);
    await select(created.id);
    return created;
  }, [client, select, upsertProject]);

  const rename = useCallback(async (projectId: string, name: string) => {
    upsertProject(await client.renameProject(projectId, name));
  }, [client, upsertProject]);

  const remove = useCallback(async (projectId: string) => {
    await client.deleteProject(projectId);
    removeProject(projectId);
    if (sessionProjectId === projectId) {
      useChatStore.getState().updateSessionProject(sessionId ?? '', { projectId: '' });
      setActiveProjectId(null);
    }
  }, [client, removeProject, sessionId, sessionProjectId, setActiveProjectId]);

  const activeProject = projects.find((project) => project.id === activeProjectId) ?? null;
  return {
    client,
    projects,
    loaded,
    loading,
    failed,
    activeProjectId,
    activeProject,
    refresh,
    select,
    create,
    rename,
    remove,
    /** The project a session created now should join. */
    newSessionProjectId: activeProjectId ?? '',
  };
}