import DefaultTheme from 'vitepress/theme-without-fonts'
import '@fontsource-variable/outfit'
import '@fontsource-variable/jetbrains-mono'
import './custom.css'

export default {
  extends: DefaultTheme,
  enhanceApp() {
    if (typeof document === 'undefined') return

    document.addEventListener('keydown', (event) => {
      if (event.key !== 'Escape') return

      const toggle = document.querySelector<HTMLButtonElement>(
        '.VPNavBarHamburger[aria-expanded="true"]',
      )
      if (!toggle) return

      event.preventDefault()
      toggle.click()
      toggle.focus()
    })
  },
}
