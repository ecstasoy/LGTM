import type { Dict } from "./zh";

export const en: Dict = {
  meta: {
    title: "LGTM — AI-assisted code review",
    description:
      "Paste any GitHub PR link and get a structured review in 30 seconds: change summary, risk analysis, and inline suggestions.",
    ogDescription: "Paste any GitHub PR link and get a structured review in 30 seconds.",
  },
  nav: {
    home: "LGTM home",
    review: "Review",
    history: "History",
    switchLocale: "Switch to Chinese",
  },
  footer: {
    copyright: "© ecstasoy 2026",
  },
  landing: {
    heroTitleTop: "Looks good to me?",
    heroTitleBottom: "Looks good to you!",
    heroLeadPrefix: "Drop in a PR link and ",
    heroLeadMiddle: " gives you a ",
    heroLeadOutputs: "summary, risks, and inline suggestions",
    heroLeadSuffix: " in thirty seconds — postable back to the PR in one click.",
    urlPlaceholder: "https://github.com/owner/repo/pull/123",
    urlAriaLabel: "GitHub pull request URL",
    submit: "Start review",
    modelPickerLabel: "Choose review model",
    perStageOff: "Per-stage model",
    perStageOn: "Per-stage (summary / risks / suggestions choose separately)",
    stageModelAriaLabel: (stage: string) => `Model for the ${stage} stage`,
    examplesLabel: "Try:",
  },
  stages: {
    summary: "Summary",
    risks: "Risks",
    suggestions: "Suggestions",
  },
};
