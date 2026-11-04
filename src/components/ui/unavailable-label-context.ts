"use client"

import { createContext, useContext } from "react"

/**
 * Words a short reason as the shared "unavailable" line, "无法使用，<reason>" (#93, #94). The Product Schema provider
 * supplies it from `ui.unavailable.label`, so the hover hint of a disabled control reads the same as an inline
 * `UnavailableNotice`. Without a provider (an isolated test or render) the reason is shown as it is.
 */
export const UnavailableLabelContext = createContext<((reason: string) => string) | null>(null)

export function useUnavailableLabel(): (reason: string) => string {
  return useContext(UnavailableLabelContext) ?? ((reason) => reason)
}
