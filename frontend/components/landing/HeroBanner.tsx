"use client";

import { useT } from "@/lib/i18n/context";

// HeroBanner h1 + 一句话副标；不含 pill / 不含三块 capability
// 标题字号 clamp 跟随视口
export function HeroBanner() {
  const t = useT();
  return (
    <header>
      <h1 className="m-0 mb-3.5 font-semibold leading-[1.1] tracking-[-0.02em] text-[clamp(28px,4.4vw,44px)]">
        {t.landing.heroTitleTop}
        <br />
        <span className="text-muted">{t.landing.heroTitleBottom}</span>
      </h1>
      <p className="m-0 mb-7 max-w-[540px] text-base leading-[1.6] text-text-2">
        {t.landing.heroLeadPrefix}
        <strong className="font-semibold text-text">LGTM</strong>
        {t.landing.heroLeadMiddle}
        <strong className="font-semibold text-text">{t.landing.heroLeadOutputs}</strong>
        {t.landing.heroLeadSuffix}
      </p>
    </header>
  );
}
