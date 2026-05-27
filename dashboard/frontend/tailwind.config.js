/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        // Trading terminal palette
        surface: {
          DEFAULT: '#0d1117',
          card: '#161b22',
          hover: '#1c2128',
          border: '#21262d',
        },
        profit: { DEFAULT: '#3fb950', dim: '#1a4a24' },
        loss:   { DEFAULT: '#f85149', dim: '#4a1a1a' },
        caution:   '#e3b341',
        defense:   '#f0883e',
        emergency: '#f85149',
        accent: '#58a6ff',
      },
      fontFamily: {
        mono: ['JetBrains Mono', 'Fira Code', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
}
