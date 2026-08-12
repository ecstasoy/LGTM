// Source of truth for all UI copy. en.ts is typed against `Dict`, so a missing key fails the build.
// Entries that need interpolation are functions.
export const zh = {
  meta: {
    title: "LGTM — AI 辅助代码评审",
    description:
      "粘贴任意 GitHub PR 链接，30 秒拿到结构化评审：变更总结 / 风险识别 / 行内建议。",
    ogDescription: "粘贴任意 GitHub PR 链接，30 秒拿到结构化评审。",
  },
  nav: {
    home: "LGTM 首页",
    review: "评审",
    history: "历史",
    switchLocale: "切换到英文",
  },
  footer: {
    copyright: "© ecstasoy 2026",
  },
  landing: {
    heroTitleTop: "Looks good to me?",
    heroTitleBottom: "Looks good to you!",
    heroLeadPrefix: "粘个 PR 链接，",
    heroLeadMiddle: " 三十秒内给你",
    heroLeadOutputs: "总结 / 风险 / 行内建议",
    heroLeadSuffix: "，可一键发到原 PR。",
    urlPlaceholder: "https://github.com/owner/repo/pull/123",
    urlAriaLabel: "GitHub Pull Request URL",
    submit: "开始评审",
    modelPickerLabel: "选择评审模型",
    perStageOff: "分阶段选择模型",
    perStageOn: "分阶段（摘要 / 风险 / 建议 各自选模型）",
    stageModelAriaLabel: (stage: string) => `${stage}阶段的模型`,
    examplesLabel: "试试：",
  },
  stages: {
    summary: "摘要",
    risks: "风险",
    suggestions: "建议",
  },
};

export type Dict = typeof zh;
