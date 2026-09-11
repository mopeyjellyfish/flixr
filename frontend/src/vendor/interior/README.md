# Interior

Vendored from https://github.com/ddoemonn/interior at commit 52988dcc82c9ef2c21bc6b288207a2b850f9b318.
MIT license; see LICENSE. Components are kept together for local, offline use.
Flixr integrations live outside this directory.

Compatibility: `toSorted` uses `slice().sort()` in presence-avatars and slider-detents for the existing ES2022 browser baseline. Vendored code is typechecked but excluded from project-specific lint rules.
`findLast` in slider-detents also uses `slice().reverse().find()` for ES2022.

`useModal` initializes its portal target on the first client render so dialogs can request input focus within the opening user gesture.
