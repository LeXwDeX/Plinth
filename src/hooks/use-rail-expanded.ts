import { useStudioStore } from '@/store/studio-store';

/**
 * Whether the stage's left rail shows names beside its icons. One setting for the 2D tool rail and the 3D
 * view bar; this browser remembers it (studio prefs `railExpanded`).
 */
export function useRailExpanded(): [boolean, (expanded: boolean) => void] {
  const expanded = useStudioStore((state) => state.railExpanded);
  const setExpanded = useStudioStore((state) => state.setRailExpanded);
  return [expanded, setExpanded];
}
