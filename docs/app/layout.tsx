import '@/app/global.css';
import { RootProvider } from 'fumadocs-ui/provider';
import type { ReactNode } from 'react';
import type { Metadata } from 'next';


export const metadata: Metadata = {
  title: {
    template: '%s | go-web-utils',
    default: 'go-web-utils - Go Web 项目实用工具库',
  },
  description: '一个用于 Go Web 项目的实用工具库，提供 IP 地址处理、验证等常用功能。',
  keywords: ['go', 'golang', 'web', 'utils', 'ip', 'tools', 'library'],
  authors: [{ name: 'woodchen-ink' }],
  creator: 'woodchen-ink',
  publisher: 'woodchen-ink',
  openGraph: {
    title: 'go-web-utils - Go Web 项目实用工具库',
    description: '一个用于 Go Web 项目的实用工具库，提供 IP 地址处理、验证等常用功能。',
    url: 'https://github.com/woodchen-ink/go-web-utils',
    siteName: 'go-web-utils',
    locale: 'zh_CN',
    type: 'website',
  },
  robots: {
    index: true,
    follow: true,
  },
};

export default function Layout({ children }: { children: ReactNode }) {
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <body className="flex flex-col min-h-screen">
        <RootProvider>{children}</RootProvider>
      </body>
    </html>
  );
}
