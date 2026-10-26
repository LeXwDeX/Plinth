/**
 * A value that is only trustworthy once something has settled (#246: a canvas's initial size is only right once
 * the chosen 2D model's settings have loaded; before that it is the Schema default). `read()` resolves with the
 * latest value as soon as it is settled, or at once when it already is.
 */
export interface SettledValue<T> {
  update(value: T, settled: boolean): void;
  read(): Promise<T>;
}

export function createSettledValue<T>(initial: T, settled = false): SettledValue<T> {
  let value = initial;
  let isSettled = settled;
  let waiting: Array<(value: T) => void> = [];
  return {
    update(next, nextSettled) {
      value = next;
      isSettled = nextSettled;
      if (!isSettled || waiting.length === 0) return;
      const resolved = waiting;
      waiting = [];
      for (const resolve of resolved) resolve(value);
    },
    read() {
      return isSettled ? Promise.resolve(value) : new Promise((resolve) => { waiting.push(resolve); });
    },
  };
}
