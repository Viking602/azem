import { ArrowUpRight, Check, MessageCircleQuestion, PencilLine, Play, ShieldCheck, X } from "lucide-react";
import { useState } from "react";
import { execute } from "../../bridge";
import { useRuntimeStore } from "../../store";
import { toolDisplayName, translator } from "../../i18n";
import type { Block, Snapshot } from "../../types";
import { Markdown } from "../Markdown";
import { ApprovalCard } from "../assistant-ui/Elements";

type PlanningQuestion = {
  id: string;
  header: string;
  question: string;
  options: Array<{ label: string; description: string; recommended?: boolean }>;
  allow_multiple?: boolean;
};

function parsePlanningQuestions(value = ""): PlanningQuestion[] {
  try {
    const parsed = JSON.parse(value);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function planningQuestionAnswered(question: PlanningQuestion, selected: Record<string, string[]>, other: Record<string, string>) {
  return (selected[question.id]?.length ?? 0) > 0 || Boolean(other[question.id]?.trim());
}

function togglePlanningSelection(question: PlanningQuestion, values: string[], label: string) {
  if (!question.allow_multiple) return [label];
  return values.includes(label) ? values.filter((item) => item !== label) : [...values, label];
}

function planningLabels(language: Snapshot["language"]) {
  if (language === "en") return {
    question: "Planning question", recommended: "Recommended", other: "Other",
    otherPlaceholder: "Type another answer", submit: "Submit answers", submitting: "Submitting…", answered: "Answered",
    planTitle: "Plan", view: "View", hide: "Hide",
    ask: "Ask about plan", revise: "Request changes", revisePrefix: "Revise the plan with these changes:\n",
    execute: "Execute plan", starting: "Starting…",
  };
  return {
    question: "规划问题", recommended: "推荐", other: "其他",
    otherPlaceholder: "输入其他答案", submit: "提交选择", submitting: "提交中…", answered: "已回答",
    planTitle: "计划", view: "查看", hide: "收起",
    ask: "提出疑问", revise: "修改计划", revisePrefix: "请根据以下要求修改计划：\n",
    execute: "执行计划", starting: "启动中…",
  };
}

export function QuestionBlock({ block, language }: { block: Block; language: Snapshot["language"] }) {
  const questions = parsePlanningQuestions(block.data?.questions);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const setError = useRuntimeStore((state) => state.setError);
  const [selected, setSelected] = useState<Record<string, string[]>>({});
  const [other, setOther] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);
  const labels = planningLabels(language);
  const pending = block.state === "pending";
  const ready = questions.length > 0 && questions.every((question) => planningQuestionAnswered(question, selected, other));
  const choose = (question: PlanningQuestion, label: string) => setSelected((current) => {
    const values = current[question.id] ?? [];
    return { ...current, [question.id]: togglePlanningSelection(question, values, label) };
  });
  const submit = async () => {
    if (!ready || submitting) return;
    setSubmitting(true);
    try {
      await execute({
        kind: "resolve_user_input",
        target: block.userInputId || block.data?.userInputId,
        sessionId: currentSessionId,
        payload: {
          answers: questions.map((question) => ({
            question_id: question.id,
            selected: selected[question.id] ?? [],
            other: other[question.id]?.trim() || undefined,
          })),
        },
      });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      setSubmitting(false);
    }
  };
  if (!questions.length) return null;
  return <article className={`planning-question ${pending ? "pending" : "resolved"}`}>
    <header><MessageCircleQuestion size={17} /><div><small>{labels.question}</small><strong>{block.title}</strong></div></header>
    <div className="planning-question-list">
      {questions.map((question) => <fieldset key={question.id} disabled={!pending || submitting}>
        <legend><span>{question.header}</span><strong>{question.question}</strong></legend>
        <div className="planning-options">
          {question.options.map((option) => {
            const active = selected[question.id]?.includes(option.label) ?? false;
            return <button key={option.label} type="button" data-active={String(active)} onClick={() => choose(question, option.label)}>
              <span className="planning-option-mark">{active ? <Check size={13} /> : null}</span>
              <span><strong>{option.label}{option.recommended ? <em>{labels.recommended}</em> : null}</strong><small>{option.description}</small></span>
            </button>;
          })}
          <label className="planning-other"><span>{labels.other}</span><input value={other[question.id] ?? ""} onChange={(event) => setOther((current) => ({ ...current, [question.id]: event.target.value }))} placeholder={labels.otherPlaceholder} /></label>
        </div>
      </fieldset>)}
    </div>
    <footer>{pending
      ? <button className="primary" disabled={!ready || submitting} onClick={submit}>{submitting ? labels.submitting : labels.submit}</button>
      : <span><Check size={14} />{labels.answered}</span>}
    </footer>
  </article>;
}

function planReviewStateLabel(state: string | undefined, language: Snapshot["language"]) {
  if (state === "superseded") return language === "en" ? "Superseded" : "已被新版本替代";
  if (state === "approved") return language === "en" ? "Executing" : "已进入执行";
  return language === "en" ? "Ready for review" : "等待审阅";
}

function planSteps(content = "") {
  const steps: Array<{ done: boolean; label: string }> = [];
  for (const line of (content || "").split("\n")) {
    const match = /^\s*[-*+]\s+\[( |x|X)\]\s*(.*)$/.exec(line);
    if (match && match[2].trim()) steps.push({ done: match[1] !== " ", label: match[2].trim() });
  }
  return steps;
}

export function PlanBlock({ block, language }: { block: Block; language: Snapshot["language"] }) {
  const running = useRuntimeStore((state) => state.running);
  const currentSessionId = useRuntimeStore((state) => state.currentSessionId);
  const setError = useRuntimeStore((state) => state.setError);
  const [submitting, setSubmitting] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const labels = planningLabels(language);
  const proposed = block.state === "proposed";
  const compose = (prefix: string) => window.dispatchEvent(new CustomEvent("azem:plan-compose", { detail: { prefix } }));
  const executePlan = async () => {
    if (!proposed || running || submitting) return;
    setSubmitting(true);
    try {
      await execute({ kind: "resolve_plan", target: block.planId || block.data?.planId, sessionId: currentSessionId, decision: "execute" });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      setSubmitting(false);
    }
  };
  const steps = planSteps(block.content);
  const doneCount = steps.filter((step) => step.done).length;
  const progress = steps.length > 0 ? Math.round((doneCount / steps.length) * 100) : 0;
  const activeIndex = block.state === "approved" ? steps.findIndex((step) => !step.done) : -1;
  const version = block.data?.version || "1";
  const stateLabel = planReviewStateLabel(block.state, language);
  const countLabel = language === "zh-CN" ? `${doneCount} / ${steps.length}` : `${doneCount} of ${steps.length}`;
  return <article className="plan-review" data-slot="agent-plan" data-state={block.state || "proposed"}>
    <header><h3>{labels.planTitle}</h3>{steps.length > 0 ? <span className="plan-review-count">{countLabel}</span> : null}</header>
    {steps.length > 0 ? <div className="plan-review-rule" role="progressbar" aria-label={countLabel} aria-valuemin={0} aria-valuemax={steps.length} aria-valuenow={doneCount}>
      <span style={{ width: `${progress}%` }} />
    </div> : null}
    {steps.length > 0 ? <ul className="plan-review-steps">
      {steps.map((step, index) => <li key={index} data-done={String(step.done)} data-active={index === activeIndex ? "true" : undefined}>
        <span className="plan-step-mark" aria-hidden="true">{step.done ? <Check size={12} strokeWidth={2.5} /> : null}</span>
        <span className="plan-step-label">{step.label}</span>
      </li>)}
    </ul> : null}
    <footer>
      <button type="button" className="plan-view" aria-expanded={expanded} onClick={() => setExpanded((value) => !value)}>
        {expanded ? labels.hide : labels.view}<ArrowUpRight size={14} />
      </button>
    </footer>
    {expanded ? <div className="plan-review-detail">
      <div className="plan-review-meta"><strong>{block.title}</strong><span>v{version} · {stateLabel}</span></div>
      <div className="plan-review-body markdown"><Markdown>{block.content || ""}</Markdown></div>
      {proposed ? <div className="plan-review-actions">
        <button onClick={() => compose("")}><MessageCircleQuestion size={14} />{labels.ask}</button>
        <button onClick={() => compose(labels.revisePrefix)}><PencilLine size={14} />{labels.revise}</button>
        <button className="primary" disabled={running || submitting} onClick={executePlan}><Play size={14} />{submitting ? labels.starting : labels.execute}</button>
      </div> : null}
    </div> : null}
  </article>;
}

export function ApprovalBlock({ block }: { block: Block }) {
  const snapshot = useRuntimeStore((state) => state.snapshot)!;
  const setError = useRuntimeStore((state) => state.setError);
  const t = translator(snapshot.language);
  const details = approvalPresentation(block, snapshot.language);
  const resolve = async (decision: string) => {
    try { await execute({ kind: "resolve_approval", target: block.approvalId, decision }); }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); }
  };
  const pending = block.state === "pending";
  const denied = block.state === "deny" || block.state === "denied";
  const resolvedLabel = denied ? t("deny") : block.state === "session" ? t("approveSession") : t("approveOnce");
  return <ApprovalCard className={pending ? "pending" : "resolved"} state={pending ? "pending" : "resolved"} data-risk={details.riskTone}>
    <header className="approval-heading"><span className="approval-icon"><ShieldCheck size={17} /></span><div><small>{t("approvalTitle")}</small><strong>{details.tool}</strong></div><span className="approval-risk">{details.riskLabel}</span></header>
    <div className="approval-target"><span>{t("approvalTarget")}</span><code>{details.target}</code></div>
    <footer className="approval-footer"><p>{details.description}</p><div className="approval-actions">{pending ? <><button onClick={() => resolve("deny")}>{t("deny")}</button><button onClick={() => resolve("once")}>{t("approveOnce")}</button><button className="primary" onClick={() => resolve("session")}>{t("approveSession")}</button></> : <span className={denied ? "denied" : "approved"}>{denied ? <X size={14} /> : <Check size={14} />}{resolvedLabel}</span>}</div></footer>
  </ApprovalCard>;
}

export function approvalPresentation(block: Block, language: Snapshot["language"]) {
  const t = translator(language);
  const riskTone = block.data?.risk === "low" || block.data?.risk === "high" ? block.data.risk : "medium";
  const riskLabel = riskTone === "low" ? t("riskLow") : riskTone === "high" ? t("riskHigh") : t("riskMedium");
  const effect = block.data?.effect;
  const description = effect === "write" ? t("approvalWrite") : effect === "external_side_effect" ? t("approvalExternal") : effect === "read_only" ? t("approvalReadOnly") : t("approvalConfirm");
  return {
    tool: block.data?.tool ? toolDisplayName(block.data.tool, language) : t("approvalOperation"),
    target: block.data?.target?.trim() || t("approvalWorkspace"),
    riskTone,
    riskLabel,
    description,
  };
}
