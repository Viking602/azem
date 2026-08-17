import { useCallback, useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { translator, type Language } from "../i18n";
import { ActionIsland } from "./beautiful-ui/Primitives";
import {
  actionIslandPosition,
  buildSelectActionPrompt,
  classifyTranscriptSelection,
  cycleIslandFocus,
  type SelectActionKind,
  type SelectionRect,
} from "./selectAction";

type IslandState = {
  text: string;
  rect: SelectionRect;
  top: number;
  left: number;
  width: number;
  height: number;
};

export function SelectActionHost({
  rootRef,
  composerRef,
  language,
  onSubmit,
}: {
  rootRef: RefObject<HTMLElement | null>;
  composerRef?: RefObject<HTMLElement | null>;
  language: Language;
  onSubmit: (text: string) => void;
}) {
  const islandRef = useRef<HTMLDivElement>(null);
  const [state, setState] = useState<IslandState | null>(null);
  const [instruction, setInstruction] = useState("");
  const t = translator(language);

  const dismiss = useCallback((clearNative = true) => {
    setState(null);
    setInstruction("");
    if (clearNative) window.getSelection()?.removeAllRanges();
  }, []);

  const place = useCallback((text: string, rect: SelectionRect, size?: { width: number; height: number }) => {
    const composerTop = composerRef?.current?.getBoundingClientRect().top;
    const width = size?.width ?? islandRef.current?.offsetWidth ?? 400;
    const height = size?.height ?? islandRef.current?.offsetHeight ?? 40;
    const pos = actionIslandPosition(rect, { width: window.innerWidth, height: window.innerHeight }, {
      islandWidth: width,
      islandHeight: height,
      composerTop,
    });
    setState({ text, rect, width, height, ...pos });
  }, [composerRef]);

  const sync = useCallback(() => {
    let classified: ReturnType<typeof classifyTranscriptSelection>;
    try {
      classified = classifyTranscriptSelection(window.getSelection(), rootRef.current);
    } catch {
      return;
    }
    if (classified.kind === "ignore") return;
    if (classified.kind === "miss") {
      if (islandRef.current?.contains(document.activeElement)) return;
      setState(null);
      setInstruction("");
      return;
    }
    place(classified.text, classified.rect);
  }, [place, rootRef]);

  useEffect(() => {
    let dragging = false;
    let frame = 0;
    const onSelectionChange = () => {
      if (dragging) return;
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(sync);
    };
    const onPointerDown = (event: PointerEvent) => {
      if (islandRef.current?.contains(event.target as Node)) return;
      dragging = true;
    };
    const onPointerUp = () => {
      if (!dragging) return;
      dragging = false;
      sync();
    };
    document.addEventListener("selectionchange", onSelectionChange);
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("pointerup", onPointerUp);
    return () => {
      cancelAnimationFrame(frame);
      document.removeEventListener("selectionchange", onSelectionChange);
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("pointerup", onPointerUp);
    };
  }, [sync]);

  useEffect(() => {
    if (!state) return;
    const root = rootRef.current;
    const onMove = () => {
      const classified = classifyTranscriptSelection(window.getSelection(), root);
      if (classified.kind === "hit") place(classified.text, classified.rect, { width: state.width, height: state.height });
    };
    root?.addEventListener("scroll", onMove, { passive: true });
    window.addEventListener("resize", onMove);
    return () => {
      root?.removeEventListener("scroll", onMove);
      window.removeEventListener("resize", onMove);
    };
  }, [place, rootRef, state]);

  useEffect(() => {
    if (!state) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      dismiss();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [dismiss, state]);

  useLayoutEffect(() => {
    if (!state) return;
    const node = islandRef.current;
    if (!node) return;
    const width = node.offsetWidth;
    const height = node.offsetHeight;
    if (width === state.width && height === state.height) return;
    place(state.text, state.rect, { width, height });
  }, [place, state]);

  useLayoutEffect(() => {
    if (!state) return;
    islandRef.current?.focus({ preventScroll: true });
  }, [state?.text]);

  const commit = (kind: SelectActionKind) => {
    if (!state) return;
    if (kind === "describe" && !instruction.trim()) return;
    onSubmit(buildSelectActionPrompt(language, kind, state.text, instruction));
    dismiss();
  };

  if (!state) return null;
  return createPortal(
    <ActionIsland
      islandRef={islandRef}
      instruction={instruction}
      onInstructionChange={setInstruction}
      onExplain={() => commit("explain")}
      onImprove={() => commit("improve")}
      onSubmit={() => commit("describe")}
      labels={{
        toolbar: t("selectAction"),
        describe: t("selectActionDescribe"),
        explain: t("selectActionExplain"),
        improve: t("selectActionImprove"),
        submit: t("selectActionSubmit"),
      }}
      style={{ top: state.top, left: state.left }}
      onKeyDown={(event) => {
        if (islandRef.current) cycleIslandFocus(islandRef.current, event.nativeEvent);
      }}
    />,
    document.body,
  );
}
