'use client';

import { create } from 'zustand';

import type { ProjectSummary } from '@/lib/projects/browser-client';

/**
 * #240: the owner's project list and the project the next new session should
 * join. The open session's own project is authoritative; this store only keeps
 * the loaded list and the last choice, so switching sessions can reset it.
 */
interface ProjectStore {
  projects: ProjectSummary[];
  /** The project selected in the switcher for the next new session. */
  activeProjectId: string | null;
  loaded: boolean;
  loading: boolean;
  failed: boolean;
  setProjects: (projects: ProjectSummary[]) => void;
  setActiveProjectId: (projectId: string | null) => void;
  upsertProject: (project: ProjectSummary) => void;
  removeProject: (projectId: string) => void;
  setStatus: (status: { loaded?: boolean; loading?: boolean; failed?: boolean }) => void;
}

export const useProjectStore = create<ProjectStore>((set) => ({
  projects: [],
  activeProjectId: null,
  loaded: false,
  loading: false,
  failed: false,
  setProjects: (projects) => set((state) => ({
    projects,
    loaded: true,
    loading: false,
    failed: false,
    activeProjectId: state.activeProjectId && projects.some((project) => project.id === state.activeProjectId)
      ? state.activeProjectId
      : null,
  })),
  setActiveProjectId: (activeProjectId) => set({ activeProjectId }),
  upsertProject: (project) => set((state) => {
    const exists = state.projects.some((item) => item.id === project.id);
    return {
      projects: exists
        ? state.projects.map((item) => item.id === project.id ? project : item)
        : [project, ...state.projects],
      loaded: true,
    };
  }),
  removeProject: (projectId) => set((state) => ({
    projects: state.projects.filter((project) => project.id !== projectId),
    activeProjectId: state.activeProjectId === projectId ? null : state.activeProjectId,
  })),
  setStatus: (status) => set(status),
}));

/** Test helper: forget the loaded projects between cases. */
export function resetProjectStore(): void {
  useProjectStore.setState({
    projects: [], activeProjectId: null, loaded: false, loading: false, failed: false,
  });
}