import Link from "next/link";

// Footer 简洁版权 + 项目链接；全局挂在 main layout
export function Footer() {
  return (
    <footer className="mt-auto border-t border-border bg-surface px-4 py-4 text-center text-[11px] text-muted">
      <span>© ecstasoy 2026</span>
      <span className="mx-2 text-faint">·</span>
      <Link
        href="https://github.com/ecstasoy/LGTM"
        target="_blank"
        rel="noreferrer"
        className="hover:text-text"
      >
        GitHub
      </Link>
    </footer>
  );
}
