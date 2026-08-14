import { useEffect, useRef } from "react";
import AnsiText from "../AnsiText";

export function ToolExecutionLog({ output, label }: { output: string; label: string }) {
  const ref = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [output]);
  return <pre ref={ref} className="tool-result tool-log" tabIndex={0} aria-label={label} aria-live="off"><AnsiText text={output} /></pre>;
}
