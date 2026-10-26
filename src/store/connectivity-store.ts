import { create } from 'zustand';

/** Browser connectivity and login state, shown by the banner at the top of the app. */
interface ConnectivityState {
  online: boolean;
  /** An API call came back 401 while the page was open. */
  sessionExpired: boolean;
  /** Raised on every return from offline; event streams listen to it and reconnect. */
  reconnectTick: number;
  setOnline: (online: boolean) => void;
  markSessionExpired: () => void;
  clearSessionExpired: () => void;
}

export const useConnectivityStore = create<ConnectivityState>((set) => ({
  online: typeof navigator === 'undefined' ? true : navigator.onLine !== false,
  sessionExpired: false,
  reconnectTick: 0,
  setOnline: (online) => set((state) => {
    if (state.online === online) return state;
    return { online, reconnectTick: online ? state.reconnectTick + 1 : state.reconnectTick };
  }),
  markSessionExpired: () => set((state) => (state.sessionExpired ? state : { sessionExpired: true })),
  clearSessionExpired: () => set((state) => (state.sessionExpired ? { sessionExpired: false } : state)),
}));
