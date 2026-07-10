/**
 * HomeHub 前端交互脚本。
 *
 * 职责：
 *  1. 暗色模式切换（localStorage 持久化）
 *  2. HTMX 全局事件处理（401 跳转登录页）
 *  3. 书签列表渲染（API 响应 → DOM）
 *  4. 登出后重定向
 */

(function () {
    'use strict';

    // ============================================================
    // 暗色模式
    // ============================================================
    const THEME_KEY = 'homehub-theme';
    const html = document.documentElement;

    function getTheme() {
        return localStorage.getItem(THEME_KEY) || 'light';
    }

    function applyTheme(theme) {
        html.setAttribute('data-theme', theme);
        localStorage.setItem(THEME_KEY, theme);
    }

    // 页面加载时应用保存的主题
    applyTheme(getTheme());

    // 主题切换按钮
    const toggleBtn = document.getElementById('theme-toggle');
    if (toggleBtn) {
        toggleBtn.addEventListener('click', function () {
            var current = html.getAttribute('data-theme');
            var next = current === 'dark' ? 'light' : 'dark';
            applyTheme(next);
        });
    }

    // ============================================================
    // HTMX 全局事件
    // ============================================================

    // 将表单提交的数据自动转为 JSON（后端所有 API 使用 ShouldBindJSON）
    document.body.addEventListener('htmx:configRequest', function (evt) {
        var verb = evt.detail.verb;
        // 仅处理 POST / PUT / PATCH（GET/DELETE 走 URL params，不转 JSON）
        if (verb !== 'post' && verb !== 'put' && verb !== 'patch') return;

        var params = evt.detail.parameters;
        if (!params || Object.keys(params).length === 0) return;

        // 将扁平参数展开为嵌套 JSON（处理 server.host → {server: {host: ...}}）
        var json = expandDotNotation(params);

        evt.detail.headers['Content-Type'] = 'application/json';
        evt.detail.body = JSON.stringify(json);
    });

    document.body.addEventListener('htmx:responseError', function (evt) {
        // 401 → 显示登录提示，不强制跳转
        if (evt.detail.xhr && evt.detail.xhr.status === 401) {
            var target = evt.detail.target;
            // 登录页面的 401 由 login.html 的 afterRequest 事件自行处理
            if (target && target.id === 'login-error') return;
            if (target && target.classList.contains('error-message')) {
                target.textContent = '请先登录';
            }
        }
        // 500 → 显示通用错误
        if (evt.detail.xhr && evt.detail.xhr.status === 500) {
            var target = evt.detail.target;
            if (target && target.classList.contains('error-message')) {
                target.textContent = '服务器内部错误';
            }
        }
    });

    // 登出后刷新页面
    document.body.addEventListener('htmx:afterRequest', function (evt) {
        var targetId = evt.detail.target ? evt.detail.target.id : '';
        if (targetId === 'logout-btn') {
            window.location.href = '/login';
        }
    });

    // ============================================================
    // 书签列表渲染（API JSON → DOM 片段）
    // ============================================================
    document.body.addEventListener('htmx:beforeSwap', function (evt) {
        var target = evt.detail.target;
        // 仅处理 bookmark-list 和 group-list 目标
        if (target.id !== 'bookmark-list' && target.id !== 'group-list') {
            return;
        }

        var xhr = evt.detail.xhr;
        if (!xhr || xhr.status !== 200) return;

        try {
            var resp = JSON.parse(xhr.responseText);
            if (resp.code !== 0 || !resp.data) return;

            var groups = resp.data;
            var html = '';

            if (!Array.isArray(groups) || groups.length === 0) {
                html = '<p class="loading">暂无书签，前往 <a href="/bookmarks">书签管理</a> 添加</p>';
            } else {
                for (var i = 0; i < groups.length; i++) {
                    var g = groups[i];
                    if (!g.bookmarks || g.bookmarks.length === 0) continue;

                    html += '<div class="bookmark-group">';
                    html += '<h3>' + escapeHtml(g.name) + '</h3>';
                    html += '<div class="bookmark-grid">';

                    for (var j = 0; j < g.bookmarks.length; j++) {
                        var b = g.bookmarks[j];
                        var icon = b.icon || '';
                        var iconHtml = icon
                            ? '<img class="bookmark-icon" src="' + escapeHtml(icon) + '" alt="" onerror="this.style.display=\'none\'">'
                            : '';
                        html += '<a class="bookmark-item" href="' + escapeHtml(b.url) + '" target="_blank">';
                        html += iconHtml;
                        html += '<span class="bookmark-title">' + escapeHtml(b.title) + '</span>';
                        html += '</a>';
                    }

                    html += '</div></div>';
                }
            }

            // 替换 HTMX 的 swap 行为：使用我们构建的 HTML
            target.innerHTML = html;
            evt.preventDefault(); // 阻止 HTMX 默认 swap
        } catch (e) {
            // JSON 解析失败，使用默认行为
        }
    });

    // ============================================================
    // 工具函数
    // ============================================================
    function escapeHtml(str) {
        var div = document.createElement('div');
        div.appendChild(document.createTextNode(str));
        return div.innerHTML;
    }

    // 将扁平点分隔键展开为嵌套对象
    // 示例：{ "server.host": "0.0.0.0", "server.port": "8080", "log.level": "info" }
    //     → { server: { host: "0.0.0.0", port: "8080" }, log: { level: "info" } }
    function expandDotNotation(flat) {
        var result = {};
        Object.keys(flat).forEach(function (key) {
            var parts = key.split('.');
            var current = result;
            for (var i = 0; i < parts.length - 1; i++) {
                if (!current[parts[i]] || typeof current[parts[i]] !== 'object') {
                    current[parts[i]] = {};
                }
                current = current[parts[i]];
            }
            current[parts[parts.length - 1]] = flat[key];
        });
        return result;
    }

    // ============================================================
    // 密码修改成功提示
    // ============================================================
    document.body.addEventListener('htmx:afterRequest', function (evt) {
        var targetId = evt.detail.target ? evt.detail.target.id : '';
        if (targetId !== 'password-error') return;

        var xhr = evt.detail.xhr;
        if (!xhr || xhr.status !== 200) return;

        try {
            var resp = JSON.parse(xhr.responseText);
            if (resp.code === 0) {
                document.getElementById('password-error').textContent = '密码修改成功，请重新登录';
                document.getElementById('password-error').style.color = 'var(--color-success)';
                // 2 秒后跳转登录页
                setTimeout(function () {
                    window.location.href = '/login';
                }, 2000);
            } else if (resp.message) {
                document.getElementById('password-error').textContent = resp.message;
                document.getElementById('password-error').style.color = 'var(--color-error)';
            }
        } catch (e) {}
    });

    // ============================================================
    // 配置保存成功/失败提示
    // ============================================================
    document.body.addEventListener('htmx:afterRequest', function (evt) {
        var targetId = evt.detail.target ? evt.detail.target.id : '';
        if (targetId !== 'config-status') return;

        var xhr = evt.detail.xhr;
        if (!xhr) return;

        var statusEl = document.getElementById('config-status');
        try {
            var resp = JSON.parse(xhr.responseText);
            if (resp.code === 0) {
                statusEl.textContent = '配置已保存并生效';
                statusEl.style.color = 'var(--color-success)';
            } else {
                statusEl.textContent = resp.message || '保存失败';
                statusEl.style.color = 'var(--color-error)';
            }
        } catch (e) {
            statusEl.textContent = '保存失败';
            statusEl.style.color = 'var(--color-error)';
        }
    });
})();
