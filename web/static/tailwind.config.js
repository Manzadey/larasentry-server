tailwind.config = {
  theme: {
    extend: {
      colors: {
        bg: 'var(--bg)', surface: 'var(--surface)', raised: 'var(--raised)', line: 'var(--line)',
        text: 'var(--text)', 'text-2': 'var(--text-2)', muted: 'var(--muted)', faint: 'var(--faint)',
        red: 'var(--red)', amber: 'var(--amber)', green: 'var(--green)', blue: 'var(--blue)'
      },
      fontFamily: {
        sans: ['IBM Plex Sans', 'system-ui', 'sans-serif'],
        mono: ['IBM Plex Mono', 'ui-monospace', 'monospace']
      }
    }
  }
}
