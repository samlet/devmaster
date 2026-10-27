import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "devmaster — 研发生产管理",
  description: "工作项、冲刺、版本与工时",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN">
      <body>{children}</body>
    </html>
  );
}
