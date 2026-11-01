# Third-party notices

Gitmora depends on separately licensed components. Their licenses continue to apply; publishing this repository does not replace those terms.

- The hosted account templates under `deploy/identity/themes/gitmora/login/` adapt Keycloak templates. They use Apache License 2.0; the full license is retained at [KEYCLOAK-LICENSE.txt](deploy/identity/themes/KEYCLOAK-LICENSE.txt). Gitmora changes the layout, styling, language/theme loading and explicit ARIA values while preserving native form contracts.
- IBM Plex Sans/Mono are provided by the pinned `@fontsource` packages under the SIL Open Font License. Build scripts copy their license files alongside generated assets.
- Tailwind CSS and its build integration use their upstream license. Build scripts include the Tailwind license with generated assets.
- The conversation reader uses `react-markdown`, `remark-gfm` and their parser dependencies under their respective upstream licenses. Builds include license texts for all shipped npm runtime dependencies in `assets/third-party-licenses.txt`.
- Go/npm dependencies are identified by `go.mod`, `go.sum`, `package.json` and `package-lock.json`. Inspect their respective upstream licenses when redistributing a build. Container bases, PostgreSQL and Keycloak retain their own distribution terms.

Entire and Git AI are independent projects. The compatibility layer reads the explicitly linked public formats in [compatibility](docs/compatibility.md); Gitmora is not an official version of either project.
