import type { RuntimeEvent, SecurityScan } from "../types";
import type { RuntimeData } from "./state";

function upsertScan(scans: SecurityScan[], scan: SecurityScan) {
  const next = scans.filter((item) => item.id !== scan.id);
  next.push(scan);
  next.sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt));
  return next;
}

export function reduceSecurityEvent(next: RuntimeData, event: RuntimeEvent): void {
  if (event.kind === "security_config_state") {
    next.securityConfig = event.securityConfig ?? null;
    return;
  }
  switch (event.kind) {
    case "security_scan_state":
      if (event.security) {
        next.securityProjection = event.security;
        next.securityProjections = { ...next.securityProjections, [event.security.scan.id]: event.security };
        next.securityScans = upsertScan(next.securityScans, event.security.scan);
        if (event.security.findings) {
          next.securityFindings = event.security.findings;
          next.securityFindingsByScan = { ...next.securityFindingsByScan, [event.security.scan.id]: event.security.findings };
        }
      }
      if (event.state === "exported" && event.data?.path) {
        next.securityExportPath = event.data.path;
      }
      break;
    case "security_scan_list":
      next.securityScansLoaded = true;
      next.securityScans = [...(event.securityScans ?? [])].sort(
        (left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt),
      );
      if (next.securityProjection && !next.securityScans.some((scan) => scan.id === next.securityProjection?.scan.id)) {
        next.securityProjection = null;
        next.securityFindings = [];
        next.selectedSecurityFinding = null;
        next.securityPatch = null;
        next.securityExportPath = "";
        next.securityPublication = null;
        next.securityProjections = {};
        next.securityFindingsByScan = {};
      }
      break;
    case "security_finding_list":
      next.securityFindings = event.securityFindings ?? [];
      if (event.data?.scanId) {
        next.securityFindingsByScan = { ...next.securityFindingsByScan, [event.data.scanId]: next.securityFindings };
      }
      if (next.selectedSecurityFinding && !next.securityFindings.some((finding) => finding.occurrenceId === next.selectedSecurityFinding?.occurrenceId)) {
        next.selectedSecurityFinding = null;
        next.securityPatch = null;
      }
      break;
    case "security_finding_detail":
      next.selectedSecurityFinding = event.securityFinding ?? null;
      break;
    case "security_patch_state":
      next.securityPatch = event.securityPatch ?? null;
      break;
    case "security_publication_state":
      next.securityPublication = { state: event.state ?? "", ...(event.data ?? {}) };
      break;
  }
}
