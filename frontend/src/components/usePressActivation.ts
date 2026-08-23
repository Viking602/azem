import {
  useCallback,
  useEffect,
  useRef,
  type MouseEventHandler,
  type PointerEventHandler,
} from "react";

export type PressActivationHandlers<Element extends HTMLElement> = {
  onPointerDown: PointerEventHandler<Element>;
  onPointerUp: PointerEventHandler<Element>;
  onMouseDown: MouseEventHandler<Element>;
  onMouseUp: MouseEventHandler<Element>;
  onClick: MouseEventHandler<Element>;
};

/**
 * Normalizes browser, WKWebView mouse, and macOS tap-to-click activation into
 * exactly one callback. In particular, WKWebView may omit pointerdown/click or
 * deliver a primary release before a later buttons=0 compatibility press pair.
 */
export default function usePressActivation<Element extends HTMLElement>(
  activate: () => void,
  disabled = false,
): PressActivationHandlers<Element> {
  const activationSequence = useRef(0);
  const pendingActivation = useRef(0);
  const activationResetTimer = useRef(0);

  useEffect(() => {
    const resetAfterRelease = () => {
      const sequence = pendingActivation.current;
      if (!sequence) return;
      if (activationResetTimer.current) window.clearTimeout(activationResetTimer.current);
      activationResetTimer.current = window.setTimeout(() => {
        if (pendingActivation.current === sequence) pendingActivation.current = 0;
        activationResetTimer.current = 0;
      }, 0);
    };
    const resetImmediately = () => {
      if (activationResetTimer.current) window.clearTimeout(activationResetTimer.current);
      activationResetTimer.current = 0;
      pendingActivation.current = 0;
    };
    document.addEventListener("pointerup", resetAfterRelease);
    document.addEventListener("mouseup", resetAfterRelease);
    document.addEventListener("pointercancel", resetImmediately, true);
    window.addEventListener("blur", resetImmediately);
    return () => {
      document.removeEventListener("pointerup", resetAfterRelease);
      document.removeEventListener("mouseup", resetAfterRelease);
      document.removeEventListener("pointercancel", resetImmediately, true);
      window.removeEventListener("blur", resetImmediately);
      resetImmediately();
    };
  }, []);

  const beginActivation = useCallback(() => {
    if (disabled) return;
    if (activationResetTimer.current) window.clearTimeout(activationResetTimer.current);
    activationResetTimer.current = 0;
    pendingActivation.current = ++activationSequence.current;
    activate();
  }, [activate, disabled]);

  const beginReleaseFallback = useCallback(() => {
    if (!disabled && !pendingActivation.current) beginActivation();
  }, [beginActivation, disabled]);

  const onPointerDown = useCallback<PointerEventHandler<Element>>((event) => {
    if (disabled || event.button !== 0 || (event.pointerType === "touch" && !event.isPrimary)) return;
    if ((event.buttons & 1) === 0) return;
    beginActivation();
  }, [beginActivation, disabled]);

  const onPointerUp = useCallback<PointerEventHandler<Element>>((event) => {
    if (disabled || event.button !== 0 || (event.pointerType === "touch" && !event.isPrimary)) return;
    beginReleaseFallback();
  }, [beginReleaseFallback, disabled]);

  const onMouseDown = useCallback<MouseEventHandler<Element>>((event) => {
    if (disabled || event.button !== 0 || (event.buttons & 1) === 0 || pendingActivation.current) return;
    beginActivation();
  }, [beginActivation, disabled]);

  const onMouseUp = useCallback<MouseEventHandler<Element>>((event) => {
    if (disabled || event.button !== 0) return;
    beginReleaseFallback();
  }, [beginReleaseFallback, disabled]);

  const onClick = useCallback<MouseEventHandler<Element>>(() => {
    if (pendingActivation.current) {
      pendingActivation.current = 0;
      if (activationResetTimer.current) window.clearTimeout(activationResetTimer.current);
      activationResetTimer.current = 0;
      return;
    }
    if (!disabled) activate();
  }, [activate, disabled]);

  return { onPointerDown, onPointerUp, onMouseDown, onMouseUp, onClick };
}
