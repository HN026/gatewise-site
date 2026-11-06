import { useEffect, useState, type ReactNode } from 'react';

export function Arrow({ down = false }: { down?: boolean }) {
  return <span aria-hidden="true" className="arrow">{down ? '↓' : '↗'}</span>;
}
export function Button({ href, children, secondary = false, download = false }: { href: string; children: ReactNode; secondary?: boolean; download?: boolean }) {
  return <a className={`button ${secondary ? 'secondary' : ''}`} href={href} download={download || undefined}>{children}<Arrow down={download} /></a>;
}
export function PageTitle({ title, children }: { title: string; children: ReactNode }) {
  return <header className="page-title"><h1>{title}</h1><p>{children}</p></header>;
}
export function Section({ title, children, id }: { title: string; children: ReactNode; id?: string }) {
  const sectionId = id ?? title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
  return <section className="doc-section" id={sectionId}><h2>{title}<a className="heading-anchor" href={`#/${location.hash.replace(/^#\//, '').split('/')[0] || 'overview'}/${sectionId}`} aria-label={`Link to ${title}`}>#</a></h2>{children}</section>;
}
export function Note({ children, label = 'Note' }: { children: ReactNode; label?: string }) {
  return <aside className="note"><span className="mono">{label}</span><div>{children}</div></aside>;
}
export function Tabs({ options, value, onChange, label }: { options: { value: string; label: string }[]; value: string; onChange: (v: string) => void; label: string }) {
  return <div className="tabs" role="group" aria-label={label}>{options.map(o => <button key={o.value} className={value === o.value ? 'selected' : ''} aria-pressed={value === o.value} onClick={() => onChange(o.value)}>{o.label}</button>)}</div>;
}
export function CodeBlock({ code, filename, language = 'go' }: { code: string; filename: string; language?: string }) {
  const [state, setState] = useState('Copy');
  useEffect(() => setState('Copy'), [code]);
  async function copy() {
    try { await navigator.clipboard.writeText(code); setState('Copied'); }
    catch { setState('Select to copy'); }
  }
  const tokens = /(\/\/[^\n]*|#[^\n]*|"[^"\n]*"|\b(?:func|package|import|if|return|nil|true|false|const|var|defer)\b)/g;
  return <div className="code-block"><div className="code-header"><span><i />{filename}</span><button onClick={copy} aria-label={`Copy ${filename}`}><span aria-live="polite">{state}</span> <span aria-hidden="true">⧉</span></button></div><pre tabIndex={0} aria-label={`${filename} ${language} example`}><code>{code.split(tokens).map((token, i) => <span key={i} className={token.startsWith('//') || token.startsWith('#') ? 'comment' : token.startsWith('"') ? 'string' : /^(?:func|package|import|if|return|nil|true|false|const|var|defer)$/.test(token) ? 'keyword' : undefined}>{token}</span>)}</code></pre></div>;
}
export function Diagram({ name, caption }: { name: string; caption: string }) {
  return <figure className="diagram"><img src={`diagrams/${name}.svg`} alt={caption} loading="lazy" /><figcaption><span>{caption}</span><a href={`diagrams/${name}.drawio`} download>Edit in draw.io <Arrow /></a></figcaption></figure>;
}
export function ImageFigure({ src, alt, children, dashboard = false }: { src: string; alt: string; children: ReactNode; dashboard?: boolean }) {
  return <figure className={`screenshot-figure ${dashboard ? 'full-dashboard' : ''}`}><a href={src} target="_blank" rel="noreferrer" aria-label={`Open full image: ${alt}`}><img src={src} alt={alt} loading="lazy" /></a><figcaption>{children}<a href={src} download>Download PNG</a></figcaption></figure>;
}
export function Table({ headers, rows }: { headers: string[]; rows: readonly (readonly string[])[] }) {
  return <div className="table-wrap" tabIndex={0}><table><thead><tr>{headers.map(h => <th key={h}>{h}</th>)}</tr></thead><tbody>{rows.map((r, i) => <tr key={i}>{r.map((c, j) => <td key={j}>{c}</td>)}</tr>)}</tbody></table></div>;
}
