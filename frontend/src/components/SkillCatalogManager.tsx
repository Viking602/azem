import { useMemo, useState } from "react";
import { Box, PackageOpen, RefreshCw, Search, Sparkles } from "lucide-react";
import { tFormat, translator, type Language } from "../i18n";
import type { SkillEntry } from "../types";

type SkillFilter = "all" | "enabled" | "disabled";

export default function SkillCatalogManager({
  skills,
  language,
  onReload,
  onSetEnabled,
}: {
  skills: SkillEntry[];
  language: Language;
  onReload: () => Promise<void>;
  onSetEnabled: (name: string, enabled: boolean) => Promise<void>;
}) {
  const t = translator(language);
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<SkillFilter>("all");
  const [pendingNames, setPendingNames] = useState<Set<string>>(new Set());
  const enabledCount = skills.filter((skill) => !skill.disabled).length;
  const disabledCount = skills.length - enabledCount;
  const visibleSkills = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase(language);
    return skills.filter((skill) => {
      if (filter === "enabled" && skill.disabled) return false;
      if (filter === "disabled" && !skill.disabled) return false;
      if (!normalized) return true;
      return `${skill.name}\n${skill.description}\n${skill.sourcePath}`.toLocaleLowerCase(language).includes(normalized);
    });
  }, [filter, language, query, skills]);

  const changeAvailability = async (skill: SkillEntry) => {
    setPendingNames((current) => new Set(current).add(skill.name));
    try {
      await onSetEnabled(skill.name, skill.disabled);
    } finally {
      setPendingNames((current) => {
        const next = new Set(current);
        next.delete(skill.name);
        return next;
      });
    }
  };

  return <section className="skill-manager" aria-label={t("skillManagerTitle")}>
    <header className="skill-manager-heading">
      <div className="skill-manager-title"><span><Sparkles size={17} /></span><div><strong>{t("skillManagerTitle")}</strong><small>{t("skillManagerHint")}</small></div></div>
      <div className="skill-manager-count"><strong>{enabledCount}</strong><span>/ {skills.length} {t("skillEnabledCount")}</span></div>
    </header>
    <div className="skill-manager-toolbar">
      <label className="skill-search"><Search size={14} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("searchSkills")} /></label>
      <div className="skill-filters" role="group" aria-label={t("filterSkills")}>
        {(["all", "enabled", "disabled"] as const).map((value) => <button type="button" key={value} className={filter === value ? "active" : ""} aria-pressed={filter === value} onClick={() => setFilter(value)}>{value === "all" ? t("skillFilterAll") : value === "enabled" ? t("skillFilterEnabled") : t("skillFilterDisabled")}<span>{value === "all" ? skills.length : value === "enabled" ? enabledCount : disabledCount}</span></button>)}
      </div>
      <button type="button" className="skill-reload" onClick={() => void onReload()} aria-label={t("reloadSkills")} title={t("reloadSkills")}><RefreshCw size={14} /></button>
    </div>
    {skills.length === 0 ? <div className="skill-manager-empty"><PackageOpen size={22} /><strong>{t("noSkills")}</strong><small>{t("noSkillsHint")}</small></div> : visibleSkills.length === 0 ? <div className="skill-manager-empty compact"><Search size={20} /><strong>{t("noMatchingSkills")}</strong></div> : <div className="skill-manager-list">
      {visibleSkills.map((skill) => {
        const pending = pendingNames.has(skill.name);
        return <article key={skill.name} className={skill.disabled ? "disabled" : ""}>
          <span className="skill-manager-icon">{skill.logoPath?.startsWith("data:image/") ? <img src={skill.logoPath} alt="" /> : <Box size={16} />}</span>
          <div className="skill-manager-copy"><strong>{skill.name}</strong><small>{skill.description || skill.sourcePath}</small><div className="skill-manager-meta"><span>{skill.bundled ? t("skillBuiltin") : t("skillExternal")}</span><span>{skill.disabled ? t("skillNotLoaded") : skill.eager ? t("skillEager") : t("skillOnDemand")}</span><span>{skill.resourceCount} {t("skillResources")}</span></div></div>
          <div className="skill-manager-state"><span>{skill.disabled ? t("skillDisabled") : t("skillEnabled")}</span><button type="button" role="switch" aria-checked={!skill.disabled} aria-label={tFormat(language, skill.disabled ? "enableSkill" : "disableSkill", { skill: skill.name })} className={`skill-state-switch ${!skill.disabled ? "on" : ""} ${pending ? "pending" : ""}`} disabled={pending} onClick={() => void changeAvailability(skill)}><i /></button></div>
        </article>;
      })}
    </div>}
  </section>;
}
