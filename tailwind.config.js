/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    // Tell Tailwind to scan all Templ files in the views folder
    "./views/**/*.templ",
    // Keep HTML just in case, and scan Go files
    "./**/*.html",
    "./**/*.go"
  ],
  theme: {
    extend: {},
  },
  plugins: [],
}
