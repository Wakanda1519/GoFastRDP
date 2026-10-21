/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./web/index.html", "./web/app.js"],
  theme: {
    extend: {
      colors: {
        brand: {
          50: "#eef4ff",
          100: "#d9e6ff",
          200: "#bcd2ff",
          300: "#8eb4ff",
          400: "#598cff",
          500: "#2f66ff",
          600: "#1a4ae6",
          700: "#1639b4",
          800: "#17318f",
          900: "#182e71",
        },
      },
      fontFamily: {
        sans: ["Segoe UI", "system-ui", "-apple-system", "sans-serif"],
      },
    },
  },
  plugins: [],
};
