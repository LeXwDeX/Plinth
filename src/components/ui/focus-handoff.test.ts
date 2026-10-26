import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { focusWhenUnblocked, releaseOutsideFocus, resetFocusHandoff, takeFocusHandoff } from '@/components/ui/focus-handoff';

// 项目测试在 node 环境运行：用最小的元素替身模拟焦点、祖先和对话框存在与否。
class FakeElement {
  focused = 0;
  blurred = 0;
  isConnected = true;
  constructor(private readonly insideDialog = false) {}
  closest(selector: string) { return selector.includes('dialog-content') && this.insideDialog ? this : null; }
  focus() { this.focused++; fakeDocument.activeElement = this; }
  blur() { this.blurred++; fakeDocument.activeElement = fakeDocument.body; }
  contains(other: unknown) { return other === this; }
}

const fakeDocument = {
  body: new FakeElement(),
  activeElement: null as FakeElement | null,
  dialogShown: false,
  querySelector() { return this.dialogShown ? new FakeElement(true) : null; },
};

beforeEach(() => {
  fakeDocument.activeElement = fakeDocument.body;
  fakeDocument.dialogShown = false;
  vi.stubGlobal('document', fakeDocument);
  vi.stubGlobal('HTMLElement', FakeElement);
  resetFocusHandoff();
});

afterEach(() => { vi.unstubAllGlobals(); });

describe('focus handoff around modal dialogs (#164)', () => {
  it('focuses at once when no dialog is on the page', () => {
    const composer = new FakeElement();
    focusWhenUnblocked(() => composer as unknown as HTMLElement);
    expect(composer.focused).toBe(1);
    expect(takeFocusHandoff()).toBeNull();
  });

  it('defers an outside target while a dialog is shown or closing, and hands it over exactly once', () => {
    fakeDocument.dialogShown = true;
    const composer = new FakeElement();
    focusWhenUnblocked(() => composer as unknown as HTMLElement);
    expect(composer.focused).toBe(0);
    expect(takeFocusHandoff()).toBe(composer);
    expect(takeFocusHandoff()).toBeNull();
  });

  it('focuses a target inside the dialog directly', () => {
    fakeDocument.dialogShown = true;
    const field = new FakeElement(true);
    focusWhenUnblocked(() => field as unknown as HTMLElement);
    expect(field.focused).toBe(1);
    expect(takeFocusHandoff()).toBeNull();
  });

  it('never hands focus to a target that left the page', () => {
    fakeDocument.dialogShown = true;
    const composer = new FakeElement();
    focusWhenUnblocked(() => composer as unknown as HTMLElement);
    composer.isConnected = false;
    expect(takeFocusHandoff()).toBeNull();
  });

  it('releases outside focus before the page is hidden and returns it as the restore target', () => {
    const trigger = new FakeElement();
    trigger.focus();
    const popup = new FakeElement(true);
    expect(releaseOutsideFocus(popup as unknown as HTMLElement)).toBe(trigger);
    expect(trigger.blurred).toBe(1);
    expect(fakeDocument.activeElement).toBe(fakeDocument.body);
  });

  it('keeps focus that is already inside the opening dialog, and ignores body', () => {
    const popup = new FakeElement(true);
    popup.focus();
    expect(releaseOutsideFocus(popup as unknown as HTMLElement)).toBeNull();
    expect(popup.blurred).toBe(0);
    fakeDocument.activeElement = fakeDocument.body;
    expect(releaseOutsideFocus(popup as unknown as HTMLElement)).toBeNull();
  });
});
