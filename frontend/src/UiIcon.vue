<script setup lang="ts">
defineProps<{name:string,size?:number}>()
const paths:Record<string,string>={
 drive:'M4 4h16v16H4z M7 8h10 M7 12h10 M7 16h4',
 plus:'M12 5v14 M5 12h14',folder:'M3 7h7l2 2h9v11H3z M3 7V4h7l2 3',file:'M6 3h8l4 4v14H6z M14 3v5h4 M9 12h6 M9 16h6',
 settings:'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8 M12 3v2 M12 19v2 M3 12h2 M19 12h2 M5.6 5.6 7 7 M17 17l1.4 1.4 M5.6 18.4 7 17 M17 7l1.4-1.4',
 logs:'M5 3h14v18H5z M8 7h8 M8 12h8 M8 17h5',refresh:'M20 10a8 8 0 0 0-14-5L3 8 M3 3v5h5 M4 14a8 8 0 0 0 14 5l3-3 M21 21v-5h-5',
 mount:'M12 16V3 M7 8l5-5 5 5 M4 14v7h16v-7',eject:'M4 16h16v5H4z M5 12l7-9 7 9z',external:'M14 3h7v7 M21 3 11 13 M10 5H3v16h16v-7',
 up:'M12 20V4 M5 11l7-7 7 7',close:'m6 6 12 12 M18 6 6 18',back:'m14 5-7 7 7 7',chevron:'m9 5 7 7-7 7',down:'m6 9 6 6 6-6',home:'m3 10 9-7 9 7 M5 9v12h14V9 M9 21v-7h6v7',
 power:'M12 2v10 M6.3 5a9 9 0 1 0 11.4 0',search:'M10.5 3a7.5 7.5 0 1 0 0 15 7.5 7.5 0 0 0 0-15 M16 16l5 5',check:'m5 12 4 4L19 6',more:'M5 12h.01 M12 12h.01 M19 12h.01',info:'M12 11v6 M12 7h.01 M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20'
}
</script>
<template><svg :width="size||18" :height="size||18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="paths[name]||paths.file"/></svg></template>
