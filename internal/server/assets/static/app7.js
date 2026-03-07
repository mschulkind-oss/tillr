// app7.js — Consistent timestamp formatting across the web UI.
// Provides "Mar 7, 2:30 PM (5m ago)" format everywhere.

(function() {
    'use strict';

    function relativeTime(date) {
        var now = new Date();
        var diffMs = now - date;
        if (diffMs < 0) return 'in the future';
        var diff = Math.floor(diffMs / 1000);
        if (diff < 60) return 'just now';
        if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
        if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
        if (diff < 2592000) return Math.floor(diff / 86400) + 'd ago';
        if (diff < 31536000) return Math.floor(diff / 2592000) + 'mo ago';
        return Math.floor(diff / 31536000) + 'y ago';
    }

    function absoluteTime(date) {
        return date.toLocaleDateString('en-US', {
            month: 'short',
            day: 'numeric'
        }) + ', ' + date.toLocaleTimeString('en-US', {
            hour: 'numeric',
            minute: '2-digit',
            hour12: true
        });
    }

    // "Mar 7, 2:30 PM (5m ago)"
    App.formatTimestamp = function(dateStr) {
        if (!dateStr) return '';
        var d = new Date(dateStr);
        if (isNaN(d.getTime())) return '';
        return absoluteTime(d) + ' (' + relativeTime(d) + ')';
    };

    // "5m ago" with title attribute containing full datetime.
    // Returns an HTML string: <span class="timestamp-auto" ...>5m ago</span>
    App.formatTimestampShort = function(dateStr) {
        if (!dateStr) return '';
        var d = new Date(dateStr);
        if (isNaN(d.getTime())) return '';
        var full = absoluteTime(d);
        var rel = relativeTime(d);
        return '<span class="timestamp-auto" data-ts="' + dateStr + '" title="' + full + '">' + rel + '</span>';
    };

    // Refresh all .timestamp-auto elements so relative times stay current.
    function refreshTimestamps() {
        var els = document.querySelectorAll('.timestamp-auto');
        for (var i = 0; i < els.length; i++) {
            var ts = els[i].getAttribute('data-ts');
            if (!ts) continue;
            var d = new Date(ts);
            if (isNaN(d.getTime())) continue;
            els[i].textContent = relativeTime(d);
            els[i].title = absoluteTime(d);
        }
    }

    App.startTimestampUpdater = function() {
        setInterval(refreshTimestamps, 60000);
    };

    // Auto-start the updater once the DOM is ready.
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', App.startTimestampUpdater);
    } else {
        App.startTimestampUpdater();
    }
})();
