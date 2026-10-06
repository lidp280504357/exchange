// The contracts' futures data for both user sites (design 2026-10-06 §3.3,
// batch F): the statistics, the overview and the liquidations, their
// chart forms and units, and the contract lists by margin type. Import
// from "@exchange/core/futures/index" (not the package index: only the
// pages that show it load it).
export * from "./data";
export * from "./series";
export * from "./list";
export * from "./liquidations";
export * from "./hooks";
