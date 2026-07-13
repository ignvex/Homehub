/**
 * router.tsx — 已废弃
 *
 * 本项目使用 HTMX 做页面导航，不使用 React Router。
 * 页面切换使用 <a hx-get="..." hx-target="..." hx-push-url="true">。
 *
 * 如需在组件内获取当前路径，使用 window.location.pathname。
 * 如需编程式导航，使用 htmx.ajax() 或 window.location.assign()。
 *
 * 此文件在后续清理中将被移除。
 */

// 此文件不再导出任何内容。
// 所有引用 router.tsx 的组件已改为直接使用 HTMX 或浏览器原生 API。
export {}
