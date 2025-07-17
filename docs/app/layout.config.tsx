import type { BaseLayoutProps } from 'fumadocs-ui/layouts/shared';

/**
 * Shared layout configurations
 *
 * you can customise layouts individually from:
 * Home Layout: app/(home)/layout.tsx
 * Docs Layout: app/docs/layout.tsx
 */
export const baseOptions: BaseLayoutProps = {
  nav: {
    title: (
      <>
        <svg
          width="24"
          height="24"
          viewBox="0 0 24 24"
          fill="none"
          xmlns="http://www.w3.org/2000/svg"
          aria-label="go-web-utils Logo"
        >
          {/* Go 语言风格的 logo */}
          <rect
            x="2"
            y="8"
            width="20"
            height="8"
            rx="4"
            fill="currentColor"
            opacity="0.2"
          />
          <circle cx="7" cy="12" r="2" fill="currentColor" />
          <circle cx="17" cy="12" r="2" fill="currentColor" />
          <path
            d="M10 12h4"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
          />
        </svg>
        go-web-utils
      </>
    ),
  },
  links: [
    {
      text: 'GitHub',
      url: 'https://github.com/woodchen-ink/go-web-utils',
      external: true,
    },
    {
      text: 'pkg.go.dev',
      url: 'https://pkg.go.dev/github.com/woodchen-ink/go-web-utils',
      external: true,
    },
  ],
};
