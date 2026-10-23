// Helpers for sync runs (state.SyncRun) shared by the History section and
// the status card.
import { formatNumber, t } from "./i18n";

/**
 * errorKindLines turns a run's errorSummary (error kind → count) into
 * translated "Network error: 3" lines, most frequent first.
 */
export function errorKindLines(summary: { [kind: string]: number | undefined } | null | undefined): string[] {
  return Object.entries(summary ?? {})
    .filter(([, n]) => (n ?? 0) > 0)
    .sort(([, a], [, b]) => (b ?? 0) - (a ?? 0))
    .map(([kind, n]) => t("history.error_kind", { kind: t(`error_kind.${kind}`), count: formatNumber(n ?? 0) }));
}
