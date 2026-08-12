"use client";

import Link from "next/link";
import { Cog, ListChecks } from "lucide-react";

import { useT } from "@/lib/i18n/context";

// HowItWorks 替代原 3 张 CapabilityCards；放最近评审之下
// 单列：左原理 / 右使用指引；窄屏自动堆叠
// 文案目标：让访客 30 秒看懂"做啥 + 怎么用"，避免读到一半放弃
export function HowItWorks() {
  const t = useT();
  return (
    <section className="mt-12 grid grid-cols-1 gap-5 md:grid-cols-2">
      <div className="rounded-lg border border-border bg-surface p-5">
        <div className="mb-3 flex items-center gap-2">
          <span className="inline-flex h-7 w-7 items-center justify-center rounded-md bg-surface-2 text-accent">
            <Cog className="h-3.5 w-3.5" />
          </span>
          <h2 className="text-sm font-semibold">{t.landing.howItWorksTitle}</h2>
        </div>
        <p className="mb-3 text-[13px] leading-[1.7] text-text-2">
          {t.landing.howItWorksPipelinePrefix}
          <strong className="font-medium text-text">{t.landing.howItWorksPipelineEmphasis}</strong>
          {t.landing.howItWorksPipelineSuffix}
        </p>
        <p className="text-[13px] leading-[1.7] text-text-2">
          {t.landing.howItWorksSuggestionPrefix}
          <code className="rounded bg-surface-2 px-1 py-0.5 font-mono text-[11px]">suggestion</code>
          {t.landing.howItWorksSuggestionSuffix}
        </p>
      </div>

      <div className="rounded-lg border border-border bg-surface p-5">
        <div className="mb-3 flex items-center gap-2">
          <span className="inline-flex h-7 w-7 items-center justify-center rounded-md bg-surface-2 text-accent">
            <ListChecks className="h-3.5 w-3.5" />
          </span>
          <h2 className="text-sm font-semibold">{t.landing.howItWorksStepsTitle}</h2>
        </div>
        <ol className="m-0 list-none space-y-2 p-0 text-[13px] leading-[1.6] text-text-2">
          <li className="flex gap-2">
            <span className="shrink-0 font-mono text-faint">1.</span>
            <span>
              {t.landing.howItWorksStep1}{" "}
              <span className="text-faint">{t.landing.howItWorksStep1Note}</span>
            </span>
          </li>
          <li className="flex gap-2">
            <span className="shrink-0 font-mono text-faint">2.</span>
            <span>
              {t.landing.howItWorksStep2}{" "}
              <span className="text-faint">{t.landing.howItWorksStep2Note}</span>
            </span>
          </li>
          <li className="flex gap-2">
            <span className="shrink-0 font-mono text-faint">3.</span>
            <span>
              {t.landing.howItWorksStep3Prefix}
              <strong className="font-medium text-text">{t.landing.howItWorksStep3EmphasisComment}</strong>
              {t.landing.howItWorksStep3Middle}
              <strong className="font-medium text-text">{t.landing.howItWorksStep3EmphasisSubmit}</strong>
            </span>
          </li>
        </ol>
        <p className="mt-4 border-t border-border pt-3 text-[12px] leading-[1.6] text-muted">
          {t.landing.howItWorksAutoPrefix}
          <Link href="/" className="text-accent underline hover:opacity-80">
            {t.landing.howItWorksAutoLinkText}
          </Link>
          {t.landing.howItWorksAutoMiddle}
          <code className="rounded bg-surface-2 px-1 py-0.5 font-mono text-[11px]">/lgtm review</code>
          {t.landing.howItWorksAutoSuffix}
        </p>
      </div>
    </section>
  );
}
