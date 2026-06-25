import type { Config } from 'tailwindcss'

export default {
  content: ['./index.html', './src/**/*.{vue,ts,tsx}'],
  darkMode: ['selector', '[data-theme="dark"]'],
  theme: {
    extend: {
      fontFamily: {
        mono: ['var(--font-mono)'],
        code: ['Consolas', 'ui-monospace', 'monospace'],
      },
      colors: {
        'bg-app': 'var(--bg-app)',
        'bg-card': 'var(--bg-card)',
        'bg-card-2': 'var(--bg-card-2)',
        'bg-muted': 'var(--bg-muted)',
        'brand-purple': 'var(--brand-purple)',
        'brand-purple-tint': 'var(--brand-purple-tint)',
        'ok': 'var(--ok)',
        'ok-tint': 'var(--ok-tint)',
        'fail': 'var(--fail)',
        'fail-tint': 'var(--fail-tint)',
        'warn': 'var(--warn)',
        'warn-tint': 'var(--warn-tint)',
        'broken': 'var(--broken)',
        'broken-tint': 'var(--broken-tint)',
        'blocked': 'var(--blocked)',
        'blocked-tint': 'var(--blocked-tint)',
        'info': 'var(--info)',
        'info-tint': 'var(--info-tint)',
        'text-primary': 'var(--text-primary)',
        'text-secondary': 'var(--text-secondary)',
        'text-muted': 'var(--text-muted)',
        'border': 'var(--border)',
        'border-strong': 'var(--border-strong)',
      },
    },
  },
  plugins: [],
} satisfies Config
