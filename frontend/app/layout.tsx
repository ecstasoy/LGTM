import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import { cookies, headers } from "next/headers";

import "./globals.css";
import { ThemeScript } from "@/components/theme-script";
import { ToastContainer } from "@/components/ui/Toast";
import { I18nProvider } from "@/lib/i18n/context";
import { en } from "@/lib/i18n/dictionaries/en";
import { zh } from "@/lib/i18n/dictionaries/zh";
import { isLocale, LOCALE_COOKIE, negotiate, type Locale } from "@/lib/i18n/locale";

// next/font 注入 CSS 变量；globals.css 的 --font-sans / --font-mono 引用这两个
const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

// An explicit cookie wins; otherwise negotiate from Accept-Language so first-time visitors land in their own language.
// Reading cookies() opts every route out of static prerendering, which costs nothing here: all page data is fetched client-side.
async function resolveServerLocale(): Promise<Locale> {
  const saved = (await cookies()).get(LOCALE_COOKIE)?.value;
  if (isLocale(saved)) return saved;
  return negotiate((await headers()).get("accept-language"));
}

export async function generateMetadata(): Promise<Metadata> {
  const locale = await resolveServerLocale();
  const t = locale === "en" ? en : zh;
  return {
    title: t.meta.title,
    description: t.meta.description,
    icons: {
      icon: [
        { url: "/brand/svg/favicon.svg", type: "image/svg+xml" },
        { url: "/brand/png/favicon-32.png", sizes: "32x32", type: "image/png" },
        { url: "/brand/png/favicon-16.png", sizes: "16x16", type: "image/png" },
      ],
      apple: [{ url: "/brand/png/apple-touch-icon-180.png", sizes: "180x180" }],
    },
    manifest: "/manifest.webmanifest",
    openGraph: {
      title: t.meta.title,
      description: t.meta.ogDescription,
      images: ["/brand/png/og-social.png"],
      type: "website",
    },
  };
}

// Root layout only owns html / body / fonts / theme script / global CSS; NavBar and width constraints live in nested layouts.
export default async function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const locale = await resolveServerLocale();
  return (
    <html
      lang={locale === "en" ? "en" : "zh-CN"}
      data-theme="light"
      data-density="comfortable"
      suppressHydrationWarning
    >
      <head>
        <ThemeScript />
      </head>
      <body className={`${geistSans.variable} ${geistMono.variable}`}>
        <I18nProvider initialLocale={locale}>
          {children}
          {/* Global webhook auto-review toast, visible from every page */}
          <ToastContainer />
        </I18nProvider>
      </body>
    </html>
  );
}
