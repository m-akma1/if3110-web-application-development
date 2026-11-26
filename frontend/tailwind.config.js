/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        'app-bg': '#121212',
        'app-sidebar': '#1c1b1e',
        'app-card': '#222126',
        'app-input': '#18171b',
        'app-border': '#333138',
        'app-border-strong': '#3f3c45',
        'app-hover': '#252429',
        'app-active': '#2c2a30',

        'brand-primary': '#ceafa3',
        'brand-primary-hover': '#c2a195',

        'sem-success': '#7dff95',
        'sem-warning': '#ffbc5e',
        'sem-danger': '#ff8080',
        'sem-info': '#87d1ff',
      },
    },
  },
  plugins: [],
}
