/**
 * #644: whether a message is sent as a canvas turn (以画板为主). Off by default: the turn works on what the
 * conversation holds (its pictures and outputs) and the board takes no part. The person turns it on for one
 * message; a selection or painted mask on the board, or `@画布` in the text, turns it on by itself (the mask
 * and the token only mean anything on the board). A turn sent as a canvas turn carries the board snapshot and
 * the Schema's `chat.context.canvas_input_source`; every other turn carries neither. Pure rules; the composer
 * keeps the per-message choice and resets it once the message has left the box.
 */
export interface BoardModeInput {
  /** The person switched it on for this message (the toggle, or 「发送到对话」 from the board). */
  chosen: boolean;
  /** The session has a dashed artboard to rasterise; without one there is no board to send. */
  artboard: boolean;
  /** Steps of the selection or mask painted on the board for this session. */
  maskSteps: number;
  /** The text names the board (`@画布`). */
  namesBoard: boolean;
}

export interface BoardMode {
  /** The message goes out as a canvas turn. */
  on: boolean;
  /** On by itself (a selection, a mask or `@画布`): the toggle shows on and cannot be turned off. */
  forced: boolean;
}

export function boardMode(input: BoardModeInput): BoardMode {
  if (!input.artboard) return { on: false, forced: false };
  const forced = input.maskSteps > 0 || input.namesBoard;
  return { on: forced || input.chosen, forced };
}

/** The turn's input source (#266): the Schema's canvas source on a canvas turn that sends a snapshot, none otherwise. */
export function boardInputSource(sendsSnapshot: boolean, canvasInputSource: string): string | undefined {
  return sendsSnapshot && canvasInputSource !== '' ? canvasInputSource : undefined;
}
