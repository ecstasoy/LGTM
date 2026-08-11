"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { HeroBanner } from "@/components/landing/HeroBanner";
import { RecentReviewsList } from "@/components/landing/RecentReviewsList";
import { UrlInputCard } from "@/components/landing/UrlInputCard";
import { useMe } from "@/lib/auth";
import { getModels, STAGES, type ModelOption, type StageKey } from "@/lib/api";

// HomePage 落地页。提交 URL → 导航 /review/streaming?url=<encoded>，流式渲染由 review 页面驱动。
// 评审本身无登录门槛；登录解锁的是历史归档 / Toast 通知 / 写回 GitHub 这些。
// 「最近评审」列表仅登录用户可见（含 repo title 等敏感字段，匿名访客不该看到）。
export default function HomePage() {
  const router = useRouter();
  const [url, setUrl] = useState("");
  const { me } = useMe();
  // L3：可选模型白名单（仅多模型时显示选择器）；默认选第一个（= 注册表默认）
  const [models, setModels] = useState<ModelOption[]>([]);
  const [model, setModel] = useState("");
  // L3 分阶段：perStage 开启时按阶段各选一个；stageModels 默认与 model 同步
  const [perStage, setPerStage] = useState(false);
  const [stageModels, setStageModels] = useState<Record<StageKey, string>>({
    summary: "",
    risks: "",
    suggestions: "",
  });

  useEffect(() => {
    let cancelled = false;
    getModels().then((opts) => {
      if (cancelled) return;
      setModels(opts);
      if (opts.length > 0) {
        const k = opts[0].key;
        setModel(k);
        setStageModels({ summary: k, risks: k, suggestions: k });
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  // 主下拉是「全部」：改它同时把三个阶段同步过去（用户可再展开分阶段微调）
  function pickAll(key: string) {
    setModel(key);
    setStageModels({ summary: key, risks: key, suggestions: key });
  }

  function start(target: string) {
    const params = new URLSearchParams({ url: target.trim() });
    if (perStage) {
      for (const s of STAGES) {
        if (stageModels[s.key]) params.set(`m_${s.key}`, stageModels[s.key]);
      }
    } else if (model) {
      params.set("model", model);
    }
    router.push(`/review/streaming?${params.toString()}`);
  }

  const authenticated = !!me?.authenticated;

  return (
    <section className="mx-auto -mt-8 max-w-[720px] pt-[clamp(40px,9vh,96px)] pb-16">
      <HeroBanner />
      <UrlInputCard
        value={url}
        onChange={setUrl}
        onSubmit={start}
        models={models}
        model={model}
        onModelChange={pickAll}
        perStage={perStage}
        onTogglePerStage={() => setPerStage((p) => !p)}
        stageModels={stageModels}
        onStageModelChange={(stage, key) =>
          setStageModels((prev) => ({ ...prev, [stage]: key }))
        }
      />
      {authenticated ? <RecentReviewsList /> : null}

      {/* 简介：评审任何 PR 不需要登录；登录归档；装 App 才能写回 GitHub */}
      <p className="mt-10 max-w-[640px] text-sm leading-[1.7] text-text-2">
        粘贴任意公开仓库的 PR 链接即可立即开始评审，无需登录。GitHub 登录后可把评审归档到你的账号下、随时回看与删除。想把
        LGTM 给出的修改建议一键发回 GitHub PR 评论 / 提交 commit，需要给对应 repo 安装{" "}
        <a
          href="https://github.com/apps/lgtm-ai-reviewer"
          target="_blank"
          rel="noreferrer"
          className="text-accent underline hover:opacity-80"
        >
          LGTM App
        </a>
        。LGTM bot 装好后也会自动评审新开的 PR。
      </p>
    </section>
  );
}
