"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import type { ReactNode } from "react";
import { clearToken, type ApiUser } from "@/lib/api";

const navigation = [
  { href: "/", label: "Search" },
  { href: "/noise", label: "Noise" },
  { href: "/cost", label: "Cost" },
  { href: "/settings", label: "Settings", adminOnly: true },
];

export function AppShell({ account, children }: { account: ApiUser; children: ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();

  function logout() {
    clearToken();
    router.replace("/login");
  }

  return (
    <div className="app-frame">
      <header className="topbar">
        <Link className="brand" href="/" aria-label="Centilog home">
          <span className="brand-mark" aria-hidden="true">C</span>
          <span className="brand-name">Centilog</span>
        </Link>
        <nav className="main-nav" aria-label="Main navigation">
          {navigation
            .filter((item) => !item.adminOnly || account.role === "admin")
            .map((item) => {
              const active = item.href === "/" ? pathname === "/" : pathname.startsWith(item.href);
              return (
                <Link className={`nav-link${active ? " nav-link-active" : ""}`} href={item.href} key={item.href}>
                  <span>{item.label}</span>
                </Link>
              );
            })}
        </nav>
        <div className="topbar-right">
          <span className="role-chip">{account.role}</span>
          <button className="logout-button" onClick={logout} title="Log out">
            Log out
          </button>
        </div>
      </header>
      <main className="page-wrap">{children}</main>
    </div>
  );
}