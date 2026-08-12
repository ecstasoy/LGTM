"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { History } from "lucide-react";

import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n/context";
import { BrandMark } from "./BrandMark";
import { LocaleToggle } from "./LocaleToggle";
import { ThemeToggle } from "./theme-toggle";
import { UserMenu } from "./auth/UserMenu";

// Top navigation bar with LGTM brand mark, navigation links, and right-hand controls (user menu, locale toggle, theme toggle).
export function NavBar() {
  const pathname = usePathname();
  const isReview = pathname === "/" || pathname.startsWith("/review");
  const isHistory = pathname.startsWith("/history");
  const t = useT();

  return (
    <header className="flex h-[52px] flex-shrink-0 items-center gap-3 border-b border-border bg-surface px-4">
      <Link href="/" className="flex items-center" aria-label={t.nav.home}>
        <BrandMark size={24} animate />
      </Link>

      <nav className="ml-2.5 flex items-center gap-0.5">
        <NavLink href="/" active={isReview}>
          {t.nav.review}
        </NavLink>
        <NavLink href="/history" active={isHistory} icon={<History className="h-[13px] w-[13px]" />}>
          {t.nav.history}
        </NavLink>
      </nav>

      <div className="ml-auto flex items-center gap-2">
        <UserMenu />
        <LocaleToggle />
        <ThemeToggle />
      </div>
    </header>
  );
}

interface NavLinkProps {
  href: string;
  active: boolean;
  icon?: React.ReactNode;
  children: React.ReactNode;
}

// NavLink ghost-variant Link，active 时给 surface-hover 背景；对应 design 的 Btn active 状态
function NavLink({ href, active, icon, children }: NavLinkProps) {
  return (
    <Link
      href={href}
      className={cn(
        "inline-flex h-7 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium transition-colors",
        active ? "bg-surface-hover text-text" : "text-text-2 hover:bg-surface-hover hover:text-text",
      )}
    >
      {icon}
      {children}
    </Link>
  );
}
