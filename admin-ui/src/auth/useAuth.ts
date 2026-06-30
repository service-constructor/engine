import { useSyncExternalStore } from "react";
import { tokenProvider } from "./index";

// useAuth re-renders components when the auth state changes (sign in / out).
export function useAuth() {
  const isAuthenticated = useSyncExternalStore(
    (cb) => tokenProvider.subscribe(cb),
    () => tokenProvider.isAuthenticated(),
  );
  return {
    isAuthenticated,
    signOut: () => tokenProvider.signOut(),
  };
}
