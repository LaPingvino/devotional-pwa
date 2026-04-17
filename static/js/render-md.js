// Shared Markdown → HTML renderer for site-wide client-side rendering.
// Handles the subset we actually emit from our data sources:
//   ### / ## / # headers, **bold**, *italic*, blank-line paragraphs, soft line breaks.
// Inline HTML passes through (e.g. <p>…</p> from bahaiprayers.net imports).
//
// Previously duplicated in layouts/{phelps/detail,daily/list,devotional/list}.html.
window.renderMd = function (text) {
  if (!text) return '';
  return text
    .replace(/^### (.+)$/gm, '<h3>$1</h3>')
    .replace(/^## (.+)$/gm, '<h2>$1</h2>')
    .replace(/^# (.+)$/gm, '<h2>$1</h2>')
    .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
    .replace(/\*([^*\n]+)\*/g, '<em>$1</em>')
    .replace(/\n{2,}/g, '</p><p>')
    .replace(/\n/g, '<br>')
    .replace(/^/, '<p>').replace(/$/, '</p>');
};
