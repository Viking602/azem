import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { RefreshCw, X } from "lucide-react";
import { listUsageReport } from "../bridge";
import { tFormat, translator, type Language } from "../i18n";
import { findModelOption, modelDisplayName, providerDisplayName, useRuntimeStore, type ModelOption } from "../store";
import type { UsageDay, UsageKindRow, UsageModelRow, UsageReport, UsageScope } from "../types";
import ProviderIcon from "./ProviderIcon";

const KIND_KEYS: Record<string, "usageKindMain" | "usageKindSubagent" | "usageKindCompaction" | "usageKindTeam" | "usageKindReview"> = {
  main: "usageKindMain",
  subagent: "usageKindSubagent",
  compaction: "usageKindCompaction",
  team: "usageKindTeam",
  review: "usageKindReview",
};

export type ActivityGrain = "daily" | "weekly" | "cumulative";

export type HeatCell = {
  date: string;
  tokens: number;
  inRange: boolean;
};

export type HeatMonth = {
  key: string;
  label: string;
  weekIndex: number;
};

export type ActivityHeatmap = {
  weeks: HeatCell[][];
  months: HeatMonth[];
  peak: number;
};

type HeatTip = {
  text: string;
  left: number;
  top: number;
  below: boolean;
};

export function formatUsageCount(value: number, language: Language): string {
  if (!Number.isFinite(value)) return "—";
  const compact = (amount: number, maxFraction: number) => {
    const abs = Math.abs(amount);
    const digits = abs >= 100 ? 0 : abs >= 10 ? 1 : maxFraction;
    return String(Number(amount.toFixed(digits)));
  };
  const abs = Math.abs(value);
  if (language === "zh-CN") {
    if (abs >= 100_000_000) return `${compact(value / 100_000_000, 2)}亿`;
    if (abs >= 10_000) return `${compact(value / 10_000, 1)}万`;
    return value.toLocaleString("zh-CN");
  }
  if (abs >= 1_000_000_000) return `${compact(value / 1_000_000_000, 1)}B`;
  if (abs >= 1_000_000) return `${compact(value / 1_000_000, 1)}M`;
  if (abs >= 1_000) return `${compact(value / 1_000, 1)}K`;
  return value.toLocaleString("en");
}

export function formatUsageDuration(ms: number, language: Language): string {
  if (!Number.isFinite(ms) || ms <= 0) return "—";
  const totalMinutes = Math.round(ms / 60_000);
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (language === "zh-CN") {
    if (hours > 0 && minutes > 0) return `${hours} 小时 ${minutes} 分钟`;
    if (hours > 0) return `${hours} 小时`;
    if (totalMinutes > 0) return `${Math.max(1, totalMinutes)} 分钟`;
    return `${Math.max(1, Math.round(ms / 1000))} 秒`;
  }
  if (hours > 0 && minutes > 0) return `${hours}h ${minutes}m`;
  if (hours > 0) return `${hours}h`;
  if (totalMinutes > 0) return `${Math.max(1, totalMinutes)}m`;
  return `${Math.max(1, Math.round(ms / 1000))}s`;
}

export function usageModelTitle(provider: string, modelId: string, modelsByProvider: Record<string, ModelOption[]>, unlabeled: string): string {
  const id = modelId.trim();
  if (!id) return unlabeled;
  const catalog = findModelOption(modelsByProvider[provider] ?? [], id);
  const name = catalog ? modelDisplayName(catalog.id, catalog.name).trim() : "";
  return name || id;
}

export function cacheHitLabel(report: Pick<UsageReport, "cacheReported" | "cacheReadTokens" | "reportedInputTokens">, language: Language): string {
  if (!report.cacheReported || report.reportedInputTokens <= 0) return "—";
  const percent = (report.cacheReadTokens / report.reportedInputTokens) * 100;
  return `${percent.toLocaleString(language === "zh-CN" ? "zh-CN" : "en", { maximumFractionDigits: 1 })}%`;
}

export function activityLevel(tokens: number, peak: number): 0 | 1 | 2 | 3 {
  if (tokens <= 0 || peak <= 0) return 0;
  const ratio = tokens / peak;
  if (ratio >= 0.66) return 3;
  if (ratio >= 0.33) return 2;
  return 1;
}

const SHARE_COLORS = [
  "color-mix(in srgb, var(--blue) 88%, var(--paper))",
  "color-mix(in srgb, var(--blue) 64%, var(--paper))",
  "color-mix(in srgb, var(--blue) 40%, var(--paper))",
  "color-mix(in srgb, var(--ink) 28%, var(--paper))",
  "color-mix(in srgb, var(--ink) 14%, var(--paper))",
];

export type UsageShare = { key: string; label: string; tokens: number; percent: number; color: string };

export function usageShares(models: UsageModelRow[], unlabeled: string): UsageShare[] {
  const total = models.reduce((sum, model) => sum + Math.max(0, model.tokens), 0);
  if (total <= 0) return [];
  const ranked = [...models].sort((left, right) => right.tokens - left.tokens);
  const head = ranked.slice(0, 4);
  const tail = ranked.slice(4).reduce((sum, model) => sum + model.tokens, 0);
  const rows = head.map((model) => ({
    key: `${model.provider}:${model.model}`,
    label: model.model.trim() || unlabeled,
    tokens: model.tokens,
  }));
  if (tail > 0) rows.push({ key: "other", label: unlabeled, tokens: tail });
  return rows.map((row, index) => ({
    ...row,
    percent: (row.tokens / total) * 100,
    color: SHARE_COLORS[index] ?? SHARE_COLORS[SHARE_COLORS.length - 1]!,
  }));
}

function useCountUp(target: number) {
  const [shown, setShown] = useState(target);
  const primed = useRef(false);
  useEffect(() => {
    if (!primed.current || shown === 0 || target === 0) {
      primed.current = true;
      setShown(target);
      return;
    }
    if (typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      setShown(target);
      return;
    }
    const from = shown;
    const started = performance.now();
    let frame = 0;
    const tick = (now: number) => {
      const progress = Math.min(1, (now - started) / 520);
      const eased = 1 - (1 - progress) ** 3;
      setShown(Math.round(from + (target - from) * eased));
      if (progress < 1) frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [target]);
  return shown;
}



export function activityHeatmap(from: string, to: string, days: UsageDay[], grain: ActivityGrain, language: Language): ActivityHeatmap {
  if (!from || !to) return { weeks: [], months: [], peak: 0 };
  const start = parseISODate(from);
  const end = parseISODate(to);
  if (!start || !end || start > end) return { weeks: [], months: [], peak: 0 };

  const byDate = new Map(days.map((day) => [day.date, Math.max(0, day.tokens)]));
  const weeks: HeatCell[][] = [];
  for (let cursor = startOfSunday(start); cursor <= end; cursor = addDays(cursor, 7)) {
    const cells: HeatCell[] = [];
    for (let weekday = 0; weekday < 7; weekday += 1) {
      const date = formatISODate(addDays(cursor, weekday));
      const inRange = date >= from && date <= to;
      cells.push({ date, tokens: inRange ? (byDate.get(date) ?? 0) : 0, inRange });
    }
    weeks.push(cells);
  }

  const weekTotals = weeks.map((week) => week.reduce((sum, cell) => sum + (cell.inRange ? cell.tokens : 0), 0));
  const cumulative = new Map<string, number>();
  let running = 0;
  for (const week of weeks) {
    for (const cell of week) {
      if (!cell.inRange) continue;
      running += cell.tokens;
      cumulative.set(cell.date, running);
    }
  }

  let peak = 0;
  const painted = weeks.map((week, weekIndex) => week.map((cell) => {
    if (!cell.inRange) return cell;
    let tokens = cell.tokens;
    if (grain === "weekly") tokens = weekTotals[weekIndex] ?? 0;
    if (grain === "cumulative") tokens = cell.tokens > 0 ? (cumulative.get(cell.date) ?? 0) : 0;
    if (tokens > peak) peak = tokens;
    return { ...cell, tokens };
  }));

  const months: HeatMonth[] = [];
  painted.forEach((week, weekIndex) => {
    const first = week.find((cell) => cell.inRange);
    if (!first) return;
    const parsed = parseISODate(first.date);
    if (!parsed) return;
    const key = `${parsed.getFullYear()}-${String(parsed.getMonth() + 1).padStart(2, "0")}`;
    const startsMonth = week.some((cell) => cell.inRange && cell.date.endsWith("-01"));
    if (months.length > 0 && !startsMonth) return;
    if (months.some((month) => month.key === key)) return;
    months.push({
      key,
      label: language === "zh-CN" ? `${parsed.getMonth() + 1}月` : parsed.toLocaleString("en", { month: "short" }),
      weekIndex,
    });
  });

  return { weeks: painted, months, peak };
}

function UsageDonut({ shares, total, language, label }: { shares: UsageShare[]; total: number; language: Language; label: string }) {
  const radius = 36;
  const circumference = 2 * Math.PI * radius;
  let offset = 0;
  return <div className="usage-donut" role="img" aria-label={label}>
    <svg viewBox="0 0 88 88" width="88" height="88">
      <circle className="usage-donut-track" cx="44" cy="44" r={radius} />
      {shares.map((share) => {
        const length = Math.max(0, (share.percent / 100) * circumference);
        const current = offset;
        offset += length;
        return <circle
          key={share.key}
          className="usage-donut-arc"
          cx="44"
          cy="44"
          r={radius}
          stroke={share.color}
          strokeDasharray={`${length} ${circumference - length}`}
          strokeDashoffset={-current}
        />;
      })}
    </svg>
    <div className="usage-donut-center">
      <strong>{formatUsageCount(total, language)}</strong>
      <span>TOKENS</span>
    </div>
  </div>;
}

export default function UsageSettings({ language, onError }: { language: Language; onError: (message: string) => void }) {
  const report = useRuntimeStore((state) => state.usageReport);
  const t = translator(language);
  const [scope, setScope] = useState<UsageScope>("project");
  const [grain, setGrain] = useState<ActivityGrain>("daily");
  const [selectedDay, setSelectedDay] = useState<HeatCell | null>(null);
  const [loading, setLoading] = useState(!report);

  const applyReport = useCallback((nextScope: UsageScope) => {
    setLoading(true);
    return listUsageReport(nextScope).then((snapshot) => {
      useRuntimeStore.getState().applyEvents([{ sequence: 0, kind: "usage_report", state: "listed", usageReport: snapshot }]);
    }).catch((cause) => {
      onError(cause instanceof Error ? cause.message : String(cause));
    }).finally(() => setLoading(false));
  }, [onError]);

  useEffect(() => {
    void applyReport(scope);
  }, [applyReport, scope]);

  const heatmap = useMemo(
    () => activityHeatmap(report?.from ?? "", report?.to ?? "", report?.days ?? [], grain, language),
    [grain, language, report],
  );
  const heatmapRef = useRef<HTMLDivElement>(null);
  const [heatTip, setHeatTip] = useState<HeatTip | null>(null);
  const kindTotal = report?.kinds.reduce((sum, row) => sum + row.tokens, 0) ?? 0;

  useEffect(() => {
    setHeatTip(null);
    setSelectedDay(null);
  }, [grain, report?.from, report?.to, report?.scope]);
  const counted = useCountUp(report && !report.empty ? report.totalTokens : 0);
  const shares = useMemo(
    () => usageShares(report?.models ?? [], t("usageOtherModels")),
    [report?.models, t],
  );

  const showHeatTip = useCallback((cell: HeatCell, week: HeatCell[], target: HTMLElement) => {
    const host = heatmapRef.current;
    if (!host || !cell.inRange) return;
    setHeatTip({
      text: heatCellLabel(cell, week, grain, language, t("usageLegendNone")),
      ...placeHeatTip(target, host),
    });
  }, [grain, language, t]);


  return <div className="usage-settings">
    <div className="usage-toolbar">
      <div className="appearance-segmented" role="radiogroup" aria-label={t("usageScopeLabel")}>
        <button type="button" className={scope === "project" ? "selected" : ""} onClick={() => setScope("project")}>{t("usageScopeProject")}</button>
        <button type="button" className={scope === "all" ? "selected" : ""} onClick={() => setScope("all")}>{t("usageScopeAll")}</button>
      </div>
      <button type="button" className="small-button" onClick={() => void applyReport(scope)} disabled={loading}>
        <RefreshCw size={13} />{t("refresh")}
      </button>
    </div>
    {loading && !report ? <div className="settings-card settings-empty"><span className="azem-mark" />{t("usageLoading")}</div> : null}
    {report?.empty ? <section className="settings-card usage-empty">
      <strong>{t("usageEmpty")}</strong>
      <p>{t("usageEmptyHint")}</p>
    </section> : null}
    {report && !report.empty ? <div className="usage-report">
      <section className="usage-ledger" data-setting-id="usage:ledger">
        <header>
          <div>
            <strong>{t("usageLedgerTitle")}</strong>
            <small>{tFormat(language, "usageWindow", { from: report.from, to: report.to })}</small>
          </div>
        </header>
        <p className="usage-ledger-total">{formatUsageCount(counted, language)}</p>
        <p className="usage-ledger-unit">{t("usageTotalTokens")}</p>
        <div className="usage-ledger-split">
          <span>{t("usageInput")} {formatUsageCount(report.inputTokens, language)}</span>
          <span>{t("usageOutput")} {formatUsageCount(report.outputTokens, language)}</span>
          <span>{t("usageCacheRead")} {report.cacheReported ? formatUsageCount(report.cacheReadTokens, language) : "—"}</span>
          <span>{t("usageCacheWrite")} {report.cacheWriteReported ? formatUsageCount(report.cacheWriteTokens, language) : "—"}</span>
          <span>{t("usageCacheHit")} {cacheHitLabel(report, language)}</span>
        </div>
        <dl className="usage-facts">
          <div><dt>{t("usageSessions")}</dt><dd>{formatUsageCount(report.sessions, language)}</dd></div>
          <div><dt>{t("usageRuns")}</dt><dd>{formatUsageCount(report.runs, language)}</dd></div>
          <div><dt>{t("usageRequests")}</dt><dd>{formatUsageCount(report.requests, language)}</dd></div>
          <div><dt>{t("usagePeakDay")}</dt><dd>{report.peakDayTokens > 0 ? formatUsageCount(report.peakDayTokens, language) : "—"}</dd></div>
          <div><dt>{t("usageCurrentStreak")}</dt><dd>{tFormat(language, "usageDays", { n: report.currentStreak })}</dd></div>
          <div><dt>{t("usageLongestStreak")}</dt><dd>{tFormat(language, "usageDays", { n: report.longestStreak })}</dd></div>
          <div><dt>{t("usageLongestRun")}</dt><dd>{formatUsageDuration(report.longestRunMs ?? 0, language)}</dd></div>
        </dl>
      </section>
      <section className="usage-activity" data-setting-id="usage:activity">
        <header>
          <strong>{t("usageActivityTitle")}</strong>
          <div className="usage-activity-grains" role="radiogroup" aria-label={t("usageGrainLabel")}>
            {(["daily", "weekly", "cumulative"] as const).map((next) => <button
              key={next}
              type="button"
              role="radio"
              aria-checked={grain === next}
              className={grain === next ? "selected" : ""}
              onClick={() => setGrain(next)}
            >{t(grainKey(next))}</button>)}
          </div>
        </header>
        <div className="usage-activity-visual">
          <div className="usage-skyline">
            <UsageDonut shares={shares} total={counted} language={language} label={t("usageSkylineTitle")} />
            <div
              ref={heatmapRef}
              className="usage-heatmap"
              data-grain={grain}
              style={{ "--weeks": String(Math.max(1, heatmap.weeks.length)) } as CSSProperties}
            >
              <div className="usage-heatmap-grid" role="group" aria-label={t("usageActivityTitle")} onMouseLeave={() => setHeatTip(null)}>
                {heatmap.weeks.map((week, weekIndex) => <div
                  key={week[0]?.date ?? weekIndex}
                  className="usage-heatmap-week"
                  style={{ "--week": String(weekIndex) } as CSSProperties}
                >
                  {week.map((cell) => {
                    const level = cell.inRange ? activityLevel(cell.tokens, heatmap.peak) : 0;
                    if (!cell.inRange) {
                      return <i key={cell.date} className="usage-heat-cell" data-level={level} data-out="" data-date={cell.date} aria-hidden />;
                    }
                    return <button
                      key={cell.date}
                      type="button"
                      className="usage-heat-cell"
                      data-level={level}
                      data-date={cell.date}
                      data-selected={selectedDay?.date === cell.date ? "" : undefined}
                      aria-pressed={selectedDay?.date === cell.date}
                      aria-label={heatCellLabel(cell, week, grain, language, t("usageLegendNone"))}
                      onClick={() => setSelectedDay((current) => current?.date === cell.date ? null : cell)}
                      onMouseEnter={(event) => showHeatTip(cell, week, event.currentTarget)}
                      onFocus={(event) => showHeatTip(cell, week, event.currentTarget)}
                      onBlur={() => setHeatTip(null)}
                    />;
                  })}
                </div>)}
              </div>
              <div className="usage-heatmap-months" aria-hidden="true">
                {heatmap.weeks.map((week, weekIndex) => {
                  const month = heatmap.months.find((item) => item.weekIndex === weekIndex);
                  return <span key={week[0]?.date ?? weekIndex}>{month?.label ?? ""}</span>;
                })}
              </div>
              {heatTip ? <div
                className="usage-heatmap-tip"
                role="tooltip"
                aria-hidden="true"
                data-placement={heatTip.below ? "below" : "above"}
                style={{ left: heatTip.left, top: heatTip.top }}
              >{heatTip.text}</div> : null}
            </div>
          </div>
          <p className="usage-skyline-hint">{t("usageSkylineHint")}</p>
          {selectedDay ? <div className="usage-day-panel" data-testid="usage-day-panel">
            <header>
              <strong>{selectedDay.date}</strong>
              <button type="button" className="small-button" onClick={() => setSelectedDay(null)} aria-label={t("usageCloseDay")}>
                <X size={12} />
              </button>
            </header>
            <p className="usage-day-total">{formatUsageCount(selectedDay.tokens, language)}</p>
            <p className="usage-day-meta">
              {tFormat(language, "usageRequestCount", { n: report.days.find((day) => day.date === selectedDay.date)?.requests ?? 0 })}
              {" · "}
              {tFormat(language, "usageDayShare", {
                percent: report.totalTokens > 0 ? ((selectedDay.tokens / report.totalTokens) * 100).toFixed(1) : "0",
              })}
            </p>
          </div> : null}
          {shares.length > 0 ? <div className="usage-share">
            <span>{t("usageByModel")}</span>
            <div className="usage-share-track" aria-hidden="true">
              {shares.map((share) => <i key={share.key} style={{ width: `${share.percent}%`, background: share.color }} />)}
            </div>
            <ul>
              {shares.map((share) => <li key={share.key}>
                <i style={{ background: share.color }} />
                <em>{share.label}</em>
                <span>{share.percent.toFixed(0)}%</span>
              </li>)}
            </ul>
          </div> : null}
          <footer className="usage-legend">
            <span>{t("usageLegendNone")}</span>
            <i data-level="0" />
            <i data-level="1" />
            <i data-level="2" />
            <i data-level="3" />
            <span>{t("usageLegendHigh")}</span>
          </footer>
        </div>
      </section>
      <div className="usage-split">
        <section className="usage-breakdown" data-setting-id="usage:kinds">
          <header>
            <strong>{t("usageKindsTitle")}</strong>
          </header>
          <ul>
            {report.kinds.map((row) => <KindRow key={row.kind} row={row} total={kindTotal} language={language} />)}
          </ul>
        </section>
        <section className="usage-breakdown" data-setting-id="usage:models">
          <header>
            <strong>{t("usageModelsTitle")}</strong>
          </header>
          {report.models.length === 0 ? <p className="usage-none">{t("usageNoModels")}</p> : <ul>
            {report.models.map((model) => <ModelRow key={`${model.provider}:${model.model}`} model={model} language={language} />)}
          </ul>}
        </section>
      </div>
      <section className="usage-breakdown usage-skills" data-setting-id="usage:skills">
        <header>
          <strong>{t("usageSkillsTitle")}</strong>
        </header>
        {report.skills.length === 0 ? <p className="usage-none">{t("usageSkillsEmpty")}</p> : <ul>
          {report.skills.map((skill) => <li key={skill.name}>
            <strong>{skill.name}</strong>
            <em>{tFormat(language, "usageSkillCount", { n: skill.activations })}</em>
          </li>)}
        </ul>}
      </section>
    </div> : null}
  </div>;
}

function ModelRow({ model, language }: { model: UsageModelRow; language: Language }) {
  const t = translator(language);
  const modelsByProvider = useRuntimeStore((state) => state.modelsByProvider);
  const modelProviders = useRuntimeStore((state) => state.modelProviders);
  const modelId = model.model.trim();
  const title = usageModelTitle(model.provider, modelId, modelsByProvider, t("usageUnlabeled"));
  const providerLabel = providerDisplayName(model.provider, modelProviders) || model.provider || "—";
  const logoID = modelProviders.find((item) => item.id === model.provider)?.modelsDevId;
  const meta = [providerLabel, modelId && modelId !== title ? modelId : "", tFormat(language, "usageRequestCount", { n: model.requests })].filter(Boolean).join(" · ");
  return <li className="usage-model-row">
    <div className="usage-model-identity">
      <ProviderIcon provider={model.provider} logoID={logoID} size={18} />
      <span>
        <strong>{title}</strong>
        <small>{meta}</small>
      </span>
    </div>
    <em>{formatUsageCount(model.tokens, language)}</em>
    <span>
      {t("usageInput")} {formatUsageCount(model.inputTokens, language)}
      {" · "}
      {t("usageOutput")} {formatUsageCount(model.outputTokens, language)}
      {" · "}
      {t("usageCacheRead")} {model.cacheReported ? formatUsageCount(model.cacheReadTokens, language) : "—"}
    </span>
  </li>;
}

function KindRow({ row, total, language }: { row: UsageKindRow; total: number; language: Language }) {
  const t = translator(language);
  const key = KIND_KEYS[row.kind];
  const width = total > 0 ? Math.max(4, Math.round((row.tokens / total) * 100)) : 0;
  return <li>
    <div>
      <strong>{key ? t(key) : row.kind}</strong>
      <small>{tFormat(language, "usageRequestCount", { n: row.requests })}</small>
    </div>
    <em>{formatUsageCount(row.tokens, language)}</em>
    <span className="usage-bar" aria-hidden="true"><i style={{ width: `${width}%` }} /></span>
  </li>;
}

function grainKey(grain: ActivityGrain): "usageGrainDaily" | "usageGrainWeekly" | "usageGrainCumulative" {
  if (grain === "weekly") return "usageGrainWeekly";
  if (grain === "cumulative") return "usageGrainCumulative";
  return "usageGrainDaily";
}

function heatCellLabel(cell: HeatCell, week: HeatCell[], grain: ActivityGrain, language: Language, none: string): string {
  const count = cell.tokens > 0 ? formatUsageCount(cell.tokens, language) : none;
  if (grain !== "weekly") return `${cell.date} · ${count}`;
  const span = week.filter((item) => item.inRange);
  const from = span[0]?.date ?? cell.date;
  const to = span[span.length - 1]?.date ?? cell.date;
  return `${from} – ${to} · ${count}`;
}

function placeHeatTip(cell: HTMLElement, host: HTMLElement): Pick<HeatTip, "left" | "top" | "below"> {
  const card = host.closest(".usage-report") ?? host.closest(".usage-activity") ?? host;
  const cardBox = card.getBoundingClientRect();
  const cellBox = cell.getBoundingClientRect();
  const left = Math.min(cardBox.right - 12, Math.max(cardBox.left + 12, cellBox.left + cellBox.width / 2));
  const below = cellBox.top - cardBox.top < 28;
  return { left, top: below ? cellBox.bottom : cellBox.top, below };
}

function parseISODate(value: string): Date | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return null;
  return new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
}

function formatISODate(value: Date): string {
  return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, "0")}-${String(value.getDate()).padStart(2, "0")}`;
}

function addDays(value: Date, amount: number): Date {
  return new Date(value.getFullYear(), value.getMonth(), value.getDate() + amount);
}

function startOfSunday(value: Date): Date {
  return addDays(value, -value.getDay());
}
