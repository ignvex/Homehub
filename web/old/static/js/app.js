/**
 * HomeHub 前端交互脚本。
 *
 * 职责：
 *  1. 暗色模式切换（localStorage 持久化）
 *  2. 用户认证状态管理（header + 页面内容显隐）
 *  3. HTMX 全局事件处理（401 提示、登出重定向）
 *  4. 书签列表渲染（API 响应 → DOM）
 *  5. 配置 / 密码修改结果提示
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

    applyTheme(getTheme());

    const toggleBtn = document.getElementById('theme-toggle');
    if (toggleBtn) {
        toggleBtn.addEventListener('click', function () {
            var current = html.getAttribute('data-theme');
            var next = current === 'dark' ? 'light' : 'dark';
            applyTheme(next);
        });
    }

    // ============================================================
    // 用户认证状态管理
    // ============================================================
    function initAuth() {
        var headerGuest = document.getElementById('header-guest');
        var headerUser = document.getElementById('header-user');
        var headerUsername = document.getElementById('header-username');

        fetch('/api/v1/auth/status')
            .then(function (r) { return r.json(); })
            .then(function (data) {
                var loggedIn = data.logged_in === true;

                // 切换 header 显隐
                if (loggedIn) {
                    if (headerGuest) headerGuest.style.display = 'none';
                    if (headerUser) headerUser.style.display = '';
                    if (headerUsername) headerUsername.textContent = data.username || '';
                } else {
                    if (headerGuest) headerGuest.style.display = '';
                    if (headerUser) headerUser.style.display = 'none';
                }

                // 切换页面级条件内容
                var guestContents = document.querySelectorAll('.auth-guest-content');
                var userContents = document.querySelectorAll('.auth-user-content');
                for (var i = 0; i < guestContents.length; i++) {
                    guestContents[i].style.display = loggedIn ? 'none' : '';
                }
                for (var j = 0; j < userContents.length; j++) {
                    userContents[j].style.display = loggedIn ? '' : 'none';
                }
            })
            .catch(function () {
                // 网络异常保持默认状态（未登录）
            });
    }

    // 监听 HTMX 页面切换，每次新内容加载后重新初始化认证状态
    document.addEventListener('htmx:afterSettle', initAuth);
    // 首次页面加载时初始化
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', initAuth);
    } else {
        initAuth();
    }

    // ============================================================
    // 书签列表渲染
    // ============================================================
    function escapeHtml(str) {
        var div = document.createElement('div');
        div.appendChild(document.createTextNode(str == null ? '' : String(str)));
        return div.innerHTML;
    }

    // 将分组列表渲染为 HTML 片段。
    function renderBookmarkGroups(groups) {
        if (!Array.isArray(groups) || groups.length === 0) {
            return '<p class="empty-state">暂无书签，前往 <a href="/bookmarks">书签管理</a> 添加</p>';
        }

        var html = '';
        for (var i = 0; i < groups.length; i++) {
            var g = groups[i];
            var bms = g.bookmarks || [];
            if (bms.length === 0) continue;

            html += '<div class="bookmark-group">';
            html += '<h3>' + escapeHtml(g.name) + '</h3>';
            html += '<div class="bookmark-grid">';

            for (var j = 0; j < bms.length; j++) {
                var b = bms[j];
                var icon = b.icon || '';
                var iconHtml = icon
                    ? '<img class="bookmark-icon" src="' + escapeHtml(icon) +
                      '" alt="" loading="lazy" onerror="this.style.display=\'none\'">'
                    : '<span class="bookmark-icon-placeholder">' +
                      escapeHtml((b.title || '?').charAt(0).toUpperCase()) + '</span>';
                html += '<a class="bookmark-item" href="' + escapeHtml(b.url) +
                        '" target="_blank" rel="noopener">';
                html += iconHtml;
                html += '<span class="bookmark-title">' + escapeHtml(b.title) + '</span>';
                html += '</a>';
            }

            html += '</div></div>';
        }
        return html;
    }

    // ============================================================
    // HTMX 全局事件
    // ============================================================

    // htmx 2.0.4 使用 FormData 提交表单，evt.detail.body 修改不再生效。
    // 后端统一使用 ShouldBind，按 Content-Type 自动选择 JSON/Form binding，
    // 无需前端手动转换。

    document.body.addEventListener('htmx:responseError', function (evt) {
        if (!evt.detail.xhr) return;

        var target = evt.detail.target;
        var status = evt.detail.xhr.status;

        // 登录页 401：解析后端错误消息并显示，触发抖动动画
        if (status === 401 && target && target.id === 'login-error') {
            try {
                var resp = JSON.parse(evt.detail.xhr.responseText);
                target.textContent = resp.message || '用户名或密码错误';
            } catch (e) {
                target.textContent = '用户名或密码错误';
            }
            target.classList.remove('shake');
            void target.offsetWidth; // 强制重排以重新触发动画
            target.classList.add('shake');
            return;
        }

        // 其他 401 → 显示登录提示，不强制跳转
        if (status === 401) {
            if (target && target.classList.contains('error-message')) {
                target.textContent = '请先登录';
            }
            return;
        }

        // 500 → 显示通用错误
        if (status === 500) {
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

    // 书签列表渲染（API JSON → DOM 片段）
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
            target.innerHTML = renderBookmarkGroups(resp.data);
            evt.preventDefault(); // 阻止 HTMX 默认 swap
        } catch (e) {
            // JSON 解析失败，使用默认行为
        }
    });

    // ============================================================
    // 工具函数
    // ============================================================
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

    // ============================================================
    // 媒体资源列表渲染
    // ============================================================
    document.body.addEventListener('htmx:beforeSwap', function (evt) {
        var target = evt.detail.target;
        if (target.id !== 'media-list') return;

        var xhr = evt.detail.xhr;
        if (!xhr || xhr.status !== 200) return;

        try {
            var resp = JSON.parse(xhr.responseText);
            if (resp.code !== 0 || !resp.data) return;
            target.innerHTML = renderMediaList(resp.data);
            evt.preventDefault();
        } catch (e) {}
    });

    function formatFileSize(bytes) {
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB';
        return (bytes / 1048576).toFixed(1) + ' MB';
    }

    function renderMediaList(assets) {
        if (!Array.isArray(assets) || assets.length === 0) {
            return '<p class="empty-state">暂无媒体资源，请上传图片或视频</p>';
        }

        var html = '<div class="media-grid">';
        for (var i = 0; i < assets.length; i++) {
            var a = assets[i];
            var isImage = a.media_type === 'image';
            var previewHtml = isImage
                ? '<img src="/static/' + escapeHtml(a.file_path) +
                  '" alt="" loading="lazy" onerror="this.parentElement.innerHTML=\'<span class=video-icon>🖼</span>\'">'
                : '<span class="video-icon">🎬</span>';

            html += '<div class="media-card">';
            html += '<div class="media-card-preview">' + previewHtml + '</div>';
            html += '<div class="media-card-info">';
            html += '<div class="media-card-name" title="' + escapeHtml(a.original_name) + '">' +
                    escapeHtml(a.original_name) + '</div>';
            html += '<div class="media-card-meta">' +
                    escapeHtml(a.media_type) + ' · ' + formatFileSize(a.file_size) + '</div>';
            html += '</div>';
            html += '<div class="media-card-actions">';
            html += '<button class="btn-delete" hx-delete="/api/v1/media-assets/' + a.id +
                    '" hx-target="#media-list" hx-swap="innerHTML">删除</button>';
            html += '</div>';
            html += '</div>';
        }
        html += '</div>';
        return html;
    }

    // 文件选择预览
    var fileInput = document.getElementById('file-input');
    var uploadBtn = document.getElementById('upload-btn');
    var previewDiv = document.getElementById('upload-preview');

    if (fileInput) {
        fileInput.addEventListener('change', function () {
            var file = this.files[0];
            if (!file) return;

            if (file.type.startsWith('image/')) {
                var reader = new FileReader();
                reader.onload = function (e) {
                    previewDiv.innerHTML = '<img src="' + e.target.result + '" alt="预览">';
                    previewDiv.style.display = 'block';
                };
                reader.readAsDataURL(file);
            } else if (file.type.startsWith('video/')) {
                var url = URL.createObjectURL(file);
                previewDiv.innerHTML = '<video src="' + url + '" controls muted></video>';
                previewDiv.style.display = 'block';
            } else {
                previewDiv.style.display = 'none';
            }

            if (uploadBtn) uploadBtn.style.display = 'inline-block';
        });
    }

    // 拖拽上传
    var dropZone = document.getElementById('drop-zone');
    if (dropZone) {
        dropZone.addEventListener('dragover', function (e) {
            e.preventDefault();
            e.stopPropagation();
            dropZone.classList.add('drag-over');
        });
        dropZone.addEventListener('dragleave', function (e) {
            e.preventDefault();
            e.stopPropagation();
            dropZone.classList.remove('drag-over');
        });
        dropZone.addEventListener('drop', function (e) {
            e.preventDefault();
            e.stopPropagation();
            dropZone.classList.remove('drag-over');

            var files = e.dataTransfer.files;
            if (files.length > 0) {
                fileInput.files = files;
                fileInput.dispatchEvent(new Event('change'));
            }
        });
    }

    // 上传错误处理
    document.body.addEventListener('htmx:afterRequest', function (evt) {
        var target = evt.detail.target;
        if (target.id !== 'media-list') return;

        var xhr = evt.detail.xhr;
        var errorEl = document.getElementById('upload-error');
        if (!errorEl) return;

        if (xhr && xhr.status === 200) {
            errorEl.textContent = '';
            if (uploadBtn) uploadBtn.style.display = 'none';
            if (previewDiv) previewDiv.style.display = 'none';
            if (fileInput) fileInput.value = '';
        } else if (xhr && xhr.status >= 400) {
            try {
                var resp = JSON.parse(xhr.responseText);
                errorEl.textContent = resp.message || '上传失败';
            } catch (e) {
                errorEl.textContent = '上传失败';
            }
        }
    });

    // ============================================================
    // 背景渲染
    // ============================================================
    var bgLoadTimer = null;
    var bgCurrentIndex = 0;
    var bgAssetsCache = null;

    function initBackground() {
        // 只在首页加载背景
        var isHome = !document.getElementById('main-content') ||
                     document.getElementById('main-content').classList.contains('home-main');
        if (!isHome && window.location.pathname !== '/') return;

        fetch('/api/v1/background')
            .then(function (r) { return r.json(); })
            .then(function (resp) {
                if (resp.code !== 0 || !resp.data) return;
                var settings = resp.data;
                var ids = settings.selected_asset_ids || [];
                if (ids.length === 0) return;

                // 加载对应资源详情
                fetch('/api/v1/media-assets')
                    .then(function (r) { return r.json(); })
                    .then(function (assetsResp) {
                        if (assetsResp.code !== 0) return;
                        var allAssets = assetsResp.data || [];
                        var selected = [];
                        for (var i = 0; i < allAssets.length; i++) {
                            if (ids.indexOf(allAssets[i].id) !== -1) {
                                selected.push(allAssets[i]);
                            }
                        }
                        if (selected.length === 0) return;

                        bgAssetsCache = selected;
                        applyBackgroundMode(settings, selected);
                    });
            })
            .catch(function () {});
    }

    function applyBackgroundMode(settings, assets) {
        document.body.classList.add('has-background');
        var root = document.documentElement;
        root.style.setProperty('--bg-fade-duration', settings.fade_duration_ms + 'ms');

        var layer = document.getElementById('background-layer');
        if (!layer) {
            layer = document.createElement('div');
            layer.id = 'background-layer';
            layer.className = 'background-layer';
            document.body.insertBefore(layer, document.body.firstChild);
        }

        function showAsset(index) {
            var asset = assets[index];
            if (!asset) return;

            var isImage = asset.media_type === 'image';
            var effectClass = settings.transition_effect === 'slide' ? 'slide-in' : 'fade-in';

            if (isImage) {
                layer.innerHTML = '<img src="/static/' + asset.file_path +
                    '" alt="" class="' + effectClass + '">';
            } else {
                // 视频背景（首帧 + mute + loop）
                layer.innerHTML = '<video src="/static/' + asset.file_path +
                    '" autoplay muted loop playsinline class="' + effectClass + '"></video>';
            }

            // 移除动画类以便下次重新触发
            var el = layer.firstElementChild;
            if (el) {
                el.addEventListener('animationend', function () {
                    el.classList.remove('fade-in', 'slide-in');
                }, { once: true });
            }
        }

        if (settings.mode === 'random') {
            // 随机模式：每次加载随机选一张
            var ri = Math.floor(Math.random() * assets.length);
            showAsset(ri);
        } else if (settings.mode === 'carousel') {
            // 轮播模式：按顺序循环
            showAsset(0);
            bgCurrentIndex = 0;
            if (bgLoadTimer) clearInterval(bgLoadTimer);
            bgLoadTimer = setInterval(function () {
                bgCurrentIndex = (bgCurrentIndex + 1) % assets.length;
                showAsset(bgCurrentIndex);
            }, settings.interval_seconds * 1000);

            // 页面不可见时暂停轮播
            document.addEventListener('visibilitychange', function () {
                if (document.hidden) {
                    if (bgLoadTimer) clearInterval(bgLoadTimer);
                } else if (settings.mode === 'carousel') {
                    bgLoadTimer = setInterval(function () {
                        bgCurrentIndex = (bgCurrentIndex + 1) % assets.length;
                        showAsset(bgCurrentIndex);
                    }, settings.interval_seconds * 1000);
                }
            });
        } else {
            // 单图模式
            if (bgLoadTimer) clearInterval(bgLoadTimer);
            showAsset(0);
        }
    }

    // 初始化背景（页面加载后）
    document.addEventListener('DOMContentLoaded', function () {
        initBackground();
    });

    // HTMX 导航到首页时重新初始化背景
    document.body.addEventListener('htmx:afterSwap', function (evt) {
        var target = evt.detail.target;
        if (target && target.classList.contains('home-main')) {
            initBackground();
        }
    });
})();
