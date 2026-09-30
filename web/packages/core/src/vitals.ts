import { onCLS, onINP, onLCP, onTTFB, type Metric } from "web-vitals";

// Web vitals (design §4.4, §12.1): LCP, CLS, INP and TTFB of real visits.
// They go to the console for now; a collection endpoint can follow.

/** reportVitals logs each metric once it is final. */
export function reportVitals(site: string, report: (m: Metric) => void = log(site)): void {
  onLCP(report);
  onCLS(report);
  onINP(report);
  onTTFB(report);
}

function log(site: string) {
  return (m: Metric) => {
    const value = m.name === "CLS" ? m.value.toFixed(3) : `${Math.round(m.value)} ms`;
    console.info(`[vitals] ${site} ${m.name} ${value} (${m.rating}) ${location.pathname}`);
  };
}
