import type { Metric } from "web-vitals";

// Web vitals (design §4.4, §12.1): LCP, CLS, INP and TTFB of real visits.
// They go to the console for now; a collection endpoint can follow. The
// library loads after the first screen: its observers are buffered, so
// the page's early entries are still seen.

/** reportVitals logs each metric once it is final. */
export function reportVitals(site: string, report: (m: Metric) => void = log(site)): void {
  void import("web-vitals").then(({ onCLS, onINP, onLCP, onTTFB }) => {
    onLCP(report);
    onCLS(report);
    onINP(report);
    onTTFB(report);
  });
}

function log(site: string) {
  return (m: Metric) => {
    const value = m.name === "CLS" ? m.value.toFixed(3) : `${Math.round(m.value)} ms`;
    console.info(`[vitals] ${site} ${m.name} ${value} (${m.rating}) ${location.pathname}`);
  };
}
