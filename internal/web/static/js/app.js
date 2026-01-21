// ANSI escape sequence parser - converts ANSI color codes to HTML spans with Tailwind classes
function parseAnsi(text) {
    if (!text) return '';

    // HTML escape function to prevent XSS
    const escapeHtml = (str) => {
        return str
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#039;');
    };

    // Color mappings to Tailwind CSS classes (optimized for dark theme)
    const fgColors = {
        30: 'text-gray-900',    // Black
        31: 'text-red-400',     // Red
        32: 'text-green-400',   // Green
        33: 'text-yellow-400',  // Yellow
        34: 'text-blue-400',    // Blue
        35: 'text-purple-400',  // Magenta
        36: 'text-cyan-400',    // Cyan
        37: 'text-gray-100',    // White
        // Bright/high-intensity foreground colors
        90: 'text-gray-500',    // Bright Black (Gray)
        91: 'text-red-300',     // Bright Red
        92: 'text-green-300',   // Bright Green
        93: 'text-yellow-300',  // Bright Yellow
        94: 'text-blue-300',    // Bright Blue
        95: 'text-purple-300',  // Bright Magenta
        96: 'text-cyan-300',    // Bright Cyan
        97: 'text-white',       // Bright White
    };

    const bgColors = {
        40: 'bg-gray-900',      // Black
        41: 'bg-red-900',       // Red
        42: 'bg-green-900',     // Green
        43: 'bg-yellow-900',    // Yellow
        44: 'bg-blue-900',      // Blue
        45: 'bg-purple-900',    // Magenta
        46: 'bg-cyan-900',      // Cyan
        47: 'bg-gray-100',      // White
        // Bright/high-intensity background colors
        100: 'bg-gray-700',     // Bright Black
        101: 'bg-red-800',      // Bright Red
        102: 'bg-green-800',    // Bright Green
        103: 'bg-yellow-800',   // Bright Yellow
        104: 'bg-blue-800',     // Bright Blue
        105: 'bg-purple-800',   // Bright Magenta
        106: 'bg-cyan-800',     // Bright Cyan
        107: 'bg-gray-200',     // Bright White
    };

    // Current style state
    let currentFg = null;
    let currentBg = null;
    let isBold = false;
    let isDim = false;
    let isItalic = false;
    let isUnderline = false;

    // Result accumulator
    let result = '';
    let lastIndex = 0;

    // Regex to match ANSI escape sequences (both \x1b and \033 representations)
    // This handles the common CSI (Control Sequence Introducer) format: ESC [ params m
    const ansiRegex = /\x1b\[([0-9;]*)m/g;

    let match;
    while ((match = ansiRegex.exec(text)) !== null) {
        // Add text before this escape sequence (escaped for safety)
        if (match.index > lastIndex) {
            const textSegment = text.substring(lastIndex, match.index);
            result += wrapWithCurrentStyle(escapeHtml(textSegment));
        }

        // Parse the escape sequence parameters
        const params = match[1] ? match[1].split(';').map(p => parseInt(p, 10)) : [0];

        // Process each parameter
        for (let i = 0; i < params.length; i++) {
            const code = params[i];

            if (code === 0) {
                // Reset all styles
                currentFg = null;
                currentBg = null;
                isBold = false;
                isDim = false;
                isItalic = false;
                isUnderline = false;
            } else if (code === 1) {
                isBold = true;
            } else if (code === 2) {
                isDim = true;
            } else if (code === 3) {
                isItalic = true;
            } else if (code === 4) {
                isUnderline = true;
            } else if (code === 22) {
                // Normal intensity (neither bold nor dim)
                isBold = false;
                isDim = false;
            } else if (code === 23) {
                isItalic = false;
            } else if (code === 24) {
                isUnderline = false;
            } else if (code === 39) {
                // Default foreground color
                currentFg = null;
            } else if (code === 49) {
                // Default background color
                currentBg = null;
            } else if ((code >= 30 && code <= 37) || (code >= 90 && code <= 97)) {
                // Standard or bright foreground colors
                currentFg = fgColors[code] || null;
            } else if ((code >= 40 && code <= 47) || (code >= 100 && code <= 107)) {
                // Standard or bright background colors
                currentBg = bgColors[code] || null;
            } else if (code === 38 && params[i + 1] === 5) {
                // 256-color foreground: 38;5;N
                const colorNum = params[i + 2];
                currentFg = get256Color(colorNum, 'fg');
                i += 2; // Skip the next two parameters
            } else if (code === 48 && params[i + 1] === 5) {
                // 256-color background: 48;5;N
                const colorNum = params[i + 2];
                currentBg = get256Color(colorNum, 'bg');
                i += 2; // Skip the next two parameters
            }
        }

        lastIndex = match.index + match[0].length;
    }

    // Add remaining text after the last escape sequence
    if (lastIndex < text.length) {
        const textSegment = text.substring(lastIndex);
        result += wrapWithCurrentStyle(escapeHtml(textSegment));
    }

    return result;

    // Helper function to wrap text with current style
    function wrapWithCurrentStyle(text) {
        if (!text) return '';

        const classes = [];
        if (currentFg) classes.push(currentFg);
        if (currentBg) classes.push(currentBg);
        if (isBold) classes.push('font-bold');
        if (isDim) classes.push('opacity-60');
        if (isItalic) classes.push('italic');
        if (isUnderline) classes.push('underline');

        if (classes.length === 0) {
            return text;
        }

        return `<span class="${classes.join(' ')}">${text}</span>`;
    }

    // Helper function to get Tailwind class for 256-color mode
    function get256Color(num, type) {
        if (num === undefined || num === null) return null;

        // Standard colors (0-7)
        if (num >= 0 && num <= 7) {
            const standardMap = type === 'fg'
                ? [fgColors[30], fgColors[31], fgColors[32], fgColors[33], fgColors[34], fgColors[35], fgColors[36], fgColors[37]]
                : [bgColors[40], bgColors[41], bgColors[42], bgColors[43], bgColors[44], bgColors[45], bgColors[46], bgColors[47]];
            return standardMap[num];
        }

        // High-intensity colors (8-15)
        if (num >= 8 && num <= 15) {
            const brightMap = type === 'fg'
                ? [fgColors[90], fgColors[91], fgColors[92], fgColors[93], fgColors[94], fgColors[95], fgColors[96], fgColors[97]]
                : [bgColors[100], bgColors[101], bgColors[102], bgColors[103], bgColors[104], bgColors[105], bgColors[106], bgColors[107]];
            return brightMap[num - 8];
        }

        // 216-color cube (16-231) and grayscale (232-255)
        // Map to closest Tailwind color approximations
        if (num >= 16 && num <= 231) {
            // 6x6x6 color cube
            const n = num - 16;
            const r = Math.floor(n / 36);
            const g = Math.floor((n % 36) / 6);
            const b = n % 6;

            // Simplified mapping to Tailwind colors based on dominant channel
            if (r > g && r > b) return type === 'fg' ? 'text-red-400' : 'bg-red-900';
            if (g > r && g > b) return type === 'fg' ? 'text-green-400' : 'bg-green-900';
            if (b > r && b > g) return type === 'fg' ? 'text-blue-400' : 'bg-blue-900';
            if (r === g && r > b) return type === 'fg' ? 'text-yellow-400' : 'bg-yellow-900';
            if (r === b && r > g) return type === 'fg' ? 'text-purple-400' : 'bg-purple-900';
            if (g === b && g > r) return type === 'fg' ? 'text-cyan-400' : 'bg-cyan-900';
            return type === 'fg' ? 'text-gray-400' : 'bg-gray-700';
        }

        // Grayscale (232-255)
        if (num >= 232 && num <= 255) {
            const gray = num - 232; // 0-23
            if (gray < 6) return type === 'fg' ? 'text-gray-900' : 'bg-gray-900';
            if (gray < 12) return type === 'fg' ? 'text-gray-600' : 'bg-gray-700';
            if (gray < 18) return type === 'fg' ? 'text-gray-400' : 'bg-gray-500';
            return type === 'fg' ? 'text-gray-200' : 'bg-gray-300';
        }

        return null;
    }
}

function app() {
    return {
        entries: [],
        namespaces: [],
        containers: [],
        filters: {
            namespace: '',
            pod: '',
            container: '',
            minSeverity: 0,
            search: '',
            timeSpan: 'live',
            startTime: '',  // Custom range start (datetime-local format)
            endTime: '',    // Custom range end (datetime-local format)
            attributes: {}
        },
        tailing: true,
        connected: false,
        showShortcuts: false,
        eventSource: null,
        stats: {
            totalEntries: 0,
            diskSizeBytes: 0
        },
        maxEntries: 1000,
        oldestLoadedId: null,    // Cursor for backward pagination
        hasMoreOlder: true,      // Whether more historical entries exist
        loadingOlder: false,     // Prevent concurrent requests
        selectedEntry: null,     // Currently selected log entry for detail panel
        detailPanelOpen: false,  // Whether detail panel is visible
        showCopyToast: false,    // Whether to show "Copied" toast
        lastSeenId: null,        // Track highest seen ID to prevent duplicates on SSE reconnection
        seenIds: new Set(),      // Set of entry IDs currently in the entries array for fast dedup

        init() {
            this.loadFilters();
            this.loadStats();

            if (this.isLiveMode()) {
                this.startTailing();
            } else {
                this.loadHistoricalLogs();
            }

            // Refresh filters periodically
            setInterval(() => this.loadFilters(), 30000);
            // Refresh stats periodically
            setInterval(() => this.loadStats(), 10000);
        },

        isLiveMode() {
            return this.filters.timeSpan === 'live';
        },

        onTimeSpanChange() {
            // When switching to custom mode, set sensible defaults (last 1 hour)
            if (this.filters.timeSpan === 'custom' && !this.filters.startTime) {
                const now = new Date();
                const oneHourAgo = new Date(now.getTime() - 60 * 60 * 1000);
                // Format as datetime-local: YYYY-MM-DDTHH:mm
                this.filters.startTime = this.formatDateTimeLocal(oneHourAgo);
                this.filters.endTime = this.formatDateTimeLocal(now);
            }
            this.applyFilters();
        },

        formatDateTimeLocal(date) {
            const pad = (n) => String(n).padStart(2, '0');
            return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
        },

        async loadFilters() {
            try {
                const [nsResp, cResp] = await Promise.all([
                    fetch('/api/filters/namespaces'),
                    fetch('/api/filters/containers')
                ]);
                this.namespaces = await nsResp.json();
                this.containers = await cResp.json();
            } catch (err) {
                console.error('Failed to load filters:', err);
            }
        },

        async loadStats() {
            try {
                const resp = await fetch('/api/stats');
                this.stats = await resp.json();
            } catch (err) {
                console.error('Failed to load stats:', err);
            }
        },

        stopStreaming() {
            if (this.eventSource) {
                this.eventSource.close();
                this.eventSource = null;
            }
            this.connected = false;
        },

        async loadHistoricalLogs() {
            this.stopStreaming();

            const params = new URLSearchParams();
            if (this.filters.namespace) params.set('namespace', this.filters.namespace);
            if (this.filters.pod) params.set('pod', this.filters.pod);
            if (this.filters.container) params.set('container', this.filters.container);
            if (this.filters.minSeverity) params.set('minSeverity', this.filters.minSeverity);
            if (this.filters.search) params.set('search', this.filters.search);
            for (const [k, v] of Object.entries(this.filters.attributes)) {
                params.set(`attr.${k}`, v);
            }

            // Handle time range (custom or relative)
            if (this.filters.timeSpan === 'custom') {
                if (this.filters.startTime) {
                    params.set('startTime', new Date(this.filters.startTime).toISOString());
                }
                if (this.filters.endTime) {
                    params.set('endTime', new Date(this.filters.endTime).toISOString());
                }
            } else {
                const timeSpanMinutes = parseInt(this.filters.timeSpan);
                if (timeSpanMinutes > 0) {
                    const startTime = new Date(Date.now() - timeSpanMinutes * 60 * 1000);
                    params.set('startTime', startTime.toISOString());
                }
            }

            params.set('order', 'desc');
            params.set('limit', '100');

            try {
                const resp = await fetch(`/api/logs?${params}`);
                const data = await resp.json();

                if (data.entries && data.entries.length > 0) {
                    // Reverse to show chronological order (oldest first in array)
                    this.entries = data.entries.reverse();
                    this.oldestLoadedId = this.entries[0].id;
                    this.hasMoreOlder = data.hasMore;

                    // Populate seenIds for deduplication
                    this.seenIds = new Set(this.entries.map(e => e.id));
                    this.lastSeenId = this.entries[this.entries.length - 1].id;

                    // Scroll to bottom to show newest entries
                    this.$nextTick(() => {
                        const container = this.$refs.logContainer;
                        if (container) {
                            container.scrollTop = container.scrollHeight;
                        }
                    });
                } else {
                    this.entries = [];
                    this.oldestLoadedId = null;
                    this.hasMoreOlder = false;
                    this.seenIds = new Set();
                    this.lastSeenId = null;
                }
            } catch (err) {
                console.error('Failed to load historical logs:', err);
            }
        },

        startTailing() {
            if (this.eventSource) {
                this.eventSource.close();
            }

            const params = new URLSearchParams();
            if (this.filters.namespace) params.set('namespace', this.filters.namespace);
            if (this.filters.pod) params.set('pod', this.filters.pod);
            if (this.filters.container) params.set('container', this.filters.container);
            if (this.filters.minSeverity) params.set('minSeverity', this.filters.minSeverity);
            if (this.filters.search) params.set('search', this.filters.search);
            for (const [k, v] of Object.entries(this.filters.attributes)) {
                params.set(`attr.${k}`, v);
            }
            // Note: Live mode doesn't use time filter - streams all new entries

            // If reconnecting, pass lastSeenId to skip initial batch (server-side optimization)
            if (this.lastSeenId) {
                params.set('lastId', this.lastSeenId);
            }

            this.eventSource = new EventSource(`/api/logs/stream?${params}`);

            this.eventSource.onopen = () => {
                this.connected = true;
            };

            this.eventSource.onmessage = (e) => {
                const entry = JSON.parse(e.data);

                // Deduplicate: skip if we already have this entry (prevents duplicates on SSE reconnection)
                if (this.seenIds.has(entry.id)) {
                    return;
                }

                this.entries.push(entry);
                this.seenIds.add(entry.id);

                // Track highest seen ID for reconnection optimization
                if (this.lastSeenId === null || entry.id > this.lastSeenId) {
                    this.lastSeenId = entry.id;
                }

                // Track oldest loaded ID for backward pagination
                if (this.oldestLoadedId === null || entry.id < this.oldestLoadedId) {
                    this.oldestLoadedId = entry.id;
                }

                // Keep max entries in memory (trim oldest when tailing)
                while (this.entries.length > this.maxEntries) {
                    const removed = this.entries.shift();
                    this.seenIds.delete(removed.id);
                    // Update oldest ID when removing from front
                    if (this.entries.length > 0) {
                        this.oldestLoadedId = this.entries[0].id;
                    }
                }

                // Auto-scroll if tailing
                if (this.tailing) {
                    this.$nextTick(() => {
                        const container = this.$refs.logContainer;
                        if (container) {
                            container.scrollTop = container.scrollHeight;
                        }
                    });
                }
            };

            this.eventSource.onerror = () => {
                this.connected = false;
                // Reconnect after 2 seconds
                setTimeout(() => {
                    if (!this.connected) {
                        this.startTailing();
                    }
                }, 2000);
            };
        },

        toggleTail() {
            this.tailing = !this.tailing;
            if (this.tailing) {
                const container = this.$refs.logContainer;
                if (container) {
                    container.scrollTop = container.scrollHeight;
                }
            }
        },

        applyFilters() {
            this.entries = [];
            this.oldestLoadedId = null;
            this.hasMoreOlder = true;
            this.loadingOlder = false;
            this.lastSeenId = null;
            this.seenIds = new Set();

            if (this.isLiveMode()) {
                this.tailing = true;
                this.startTailing();
            } else {
                this.tailing = false;
                this.loadHistoricalLogs();
            }
        },

        clearLogs() {
            this.entries = [];
            this.oldestLoadedId = null;
            this.hasMoreOlder = true;
            this.lastSeenId = null;
            this.seenIds = new Set();
        },

        async loadOlderEntries() {
            if (this.loadingOlder || !this.hasMoreOlder || this.entries.length === 0) {
                return;
            }

            this.loadingOlder = true;

            // Build query params matching current filters
            const params = new URLSearchParams();
            if (this.filters.namespace) params.set('namespace', this.filters.namespace);
            if (this.filters.pod) params.set('pod', this.filters.pod);
            if (this.filters.container) params.set('container', this.filters.container);
            if (this.filters.minSeverity) params.set('minSeverity', this.filters.minSeverity);
            if (this.filters.search) params.set('search', this.filters.search);
            for (const [k, v] of Object.entries(this.filters.attributes)) {
                params.set(`attr.${k}`, v);
            }

            // Apply time range filter for historical mode
            if (!this.isLiveMode()) {
                if (this.filters.timeSpan === 'custom') {
                    if (this.filters.startTime) {
                        params.set('startTime', new Date(this.filters.startTime).toISOString());
                    }
                    if (this.filters.endTime) {
                        params.set('endTime', new Date(this.filters.endTime).toISOString());
                    }
                } else {
                    const timeSpanMinutes = parseInt(this.filters.timeSpan);
                    if (timeSpanMinutes > 0) {
                        const startTime = new Date(Date.now() - timeSpanMinutes * 60 * 1000);
                        params.set('startTime', startTime.toISOString());
                    }
                }
            }

            // Use beforeId for backward pagination with descending order
            params.set('beforeId', this.oldestLoadedId);
            params.set('order', 'desc');
            params.set('limit', '100');

            try {
                const resp = await fetch(`/api/logs?${params}`);
                const data = await resp.json();

                if (!data.entries || data.entries.length === 0) {
                    this.hasMoreOlder = false;
                } else {
                    // Preserve scroll position
                    const container = this.$refs.logContainer;
                    const prevScrollHeight = container.scrollHeight;
                    const prevScrollTop = container.scrollTop;

                    // Prepend entries (API returns newest-first, so reverse for chronological order)
                    // Filter out any duplicates that might already be in the entries array
                    const olderEntries = data.entries.reverse().filter(e => !this.seenIds.has(e.id));

                    // Add new entries to seenIds
                    olderEntries.forEach(e => this.seenIds.add(e.id));

                    this.entries = [...olderEntries, ...this.entries];

                    // Update cursor to oldest entry
                    if (olderEntries.length > 0) {
                        this.oldestLoadedId = olderEntries[0].id;
                    }
                    this.hasMoreOlder = data.hasMore;

                    // Trim from end if exceeding maxEntries (remove newest when in historical mode)
                    while (this.entries.length > this.maxEntries) {
                        const removed = this.entries.pop();
                        this.seenIds.delete(removed.id);
                    }

                    // Restore scroll position after DOM update
                    this.$nextTick(() => {
                        const newScrollHeight = container.scrollHeight;
                        container.scrollTop = prevScrollTop + (newScrollHeight - prevScrollHeight);
                    });
                }
            } catch (err) {
                console.error('Failed to load older entries:', err);
            } finally {
                this.loadingOlder = false;
            }
        },

        handleScroll(event) {
            const container = event.target;
            const scrollThreshold = 200;

            // Detect scroll to top for loading older entries (works in both modes)
            if (container.scrollTop < scrollThreshold && !this.loadingOlder && this.hasMoreOlder) {
                this.loadOlderEntries();
            }

            // Disable tailing when user scrolls up (only in Live mode)
            if (this.isLiveMode() && this.tailing) {
                const isAtBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 50;
                if (!isAtBottom) {
                    this.tailing = false;
                }
            }
        },

        handleKeydown(e) {
            // Ignore if typing in input/select
            if (e.target.tagName === 'INPUT' || e.target.tagName === 'SELECT' || e.target.tagName === 'TEXTAREA') {
                return;
            }

            // Don't interfere with browser shortcuts (Cmd+c, Ctrl+v, etc.)
            if (e.ctrlKey || e.metaKey || e.altKey) {
                return;
            }

            switch (e.key) {
                case '/':
                    e.preventDefault();
                    this.$refs.searchInput.focus();
                    break;
                case '?':
                    e.preventDefault();
                    this.showShortcuts = !this.showShortcuts;
                    break;
                case 't':
                    e.preventDefault();
                    this.toggleTail();
                    break;
                case 'c':
                    e.preventDefault();
                    this.clearLogs();
                    break;
                case 'g':
                    e.preventDefault();
                    this.$refs.logContainer.scrollTop = 0;
                    this.tailing = false;
                    break;
                case 'G':
                    e.preventDefault();
                    this.$refs.logContainer.scrollTop = this.$refs.logContainer.scrollHeight;
                    this.tailing = true;
                    break;
                case 'u':
                    e.preventDefault();
                    this.loadOlderEntries();
                    break;
                case 'j':
                    e.preventDefault();
                    if (this.detailPanelOpen) {
                        this.navigateEntry('next');
                    }
                    break;
                case 'k':
                    e.preventDefault();
                    if (this.detailPanelOpen) {
                        this.navigateEntry('prev');
                    }
                    break;
                case '1':
                case '2':
                case '3':
                case '4':
                case '5':
                case '6':
                    e.preventDefault();
                    this.filters.minSeverity = parseInt(e.key);
                    this.applyFilters();
                    break;
                case 'Escape':
                    e.preventDefault();
                    if (this.detailPanelOpen) {
                        this.closeDetailPanel();
                    } else if (this.showShortcuts) {
                        this.showShortcuts = false;
                    } else {
                        this.filters = { namespace: '', pod: '', container: '', minSeverity: 0, search: '', timeSpan: 'live', startTime: '', endTime: '', attributes: {} };
                        this.applyFilters();
                    }
                    break;
            }
        },

        formatTimestamp(nanos) {
            const date = new Date(nanos / 1000000);
            const pad = (n, w = 2) => String(n).padStart(w, '0');
            return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ` +
                   `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`;
        },

        severityLabel(s) {
            const labels = ['UNK', 'TRC', 'DBG', 'INF', 'WRN', 'ERR', 'FTL'];
            return labels[s] || 'UNK';
        },

        severityClass(s) {
            const classes = [
                'text-gray-500',    // Unknown
                'text-gray-400',    // Trace
                'text-gray-300',    // Debug
                'text-blue-400',    // Info
                'text-yellow-400',  // Warn
                'text-red-400',     // Error
                'text-red-500'      // Fatal
            ];
            return classes[s] || 'text-gray-500';
        },

        severityRowClass(s) {
            if (s >= 6) return 'bg-red-900/30';   // Fatal
            if (s >= 5) return 'bg-red-900/20';   // Error
            if (s >= 4) return 'bg-yellow-900/10'; // Warn
            return '';
        },

        // Render message with ANSI color support
        renderMessage(text) {
            if (!text) return '';
            return parseAnsi(text);
        },

        selectEntry(entry) {
            this.selectedEntry = entry;
            this.detailPanelOpen = true;
        },

        closeDetailPanel() {
            this.detailPanelOpen = false;
        },

        truncateValue(value, maxLen = 12) {
            if (!value) return '';
            if (value.length <= maxLen) return value;
            return value.substring(0, maxLen - 1) + '…';
        },

        navigateEntry(direction) {
            if (!this.selectedEntry || this.entries.length === 0) return;

            const currentIndex = this.entries.findIndex(e => e.id === this.selectedEntry.id);
            if (currentIndex === -1) return;

            const newIndex = direction === 'next'
                ? Math.min(currentIndex + 1, this.entries.length - 1)
                : Math.max(currentIndex - 1, 0);

            this.selectedEntry = this.entries[newIndex];

            // Scroll selected row into view
            this.$nextTick(() => {
                const row = this.$refs.logContainer?.querySelector(`tr[data-id="${this.selectedEntry.id}"]`);
                row?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
            });
        },

        async copyToClipboard(value) {
            if (!value) return;
            try {
                await navigator.clipboard.writeText(String(value));
                this.showCopyToast = true;
                setTimeout(() => this.showCopyToast = false, 1500);
            } catch (err) {
                console.error('Failed to copy:', err);
            }
        },

        addQuickFilter(type, key, value) {
            if (!value && value !== 0) return;
            if (type === 'namespace') {
                this.filters.namespace = value;
            } else if (type === 'pod') {
                this.filters.pod = value;
            } else if (type === 'container') {
                this.filters.container = value;
            } else if (type === 'severity') {
                this.filters.minSeverity = value;
            } else if (type === 'attr') {
                this.filters.attributes[key] = value;
            }
            this.applyFilters();
        },

        removeAttrFilter(key) {
            delete this.filters.attributes[key];
            this.applyFilters();
        },

        clearQuickFilters() {
            this.filters.pod = '';
            this.filters.attributes = {};
            this.applyFilters();
        },

        hasQuickFilters() {
            return this.filters.pod !== '' || Object.keys(this.filters.attributes).length > 0;
        }
    };
}
