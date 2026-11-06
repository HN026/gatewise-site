import { useEffect, useRef, useState, type ReactNode } from 'react';
import { configuration, metricFamilies, navigation, pageSections, type PageId } from '../data/content';

export default function Layout({ page, children }: { page: PageId; children: ReactNode }) {
  const [menu, setMenu] = useState(false);
  const [search, setSearch] = useState(false);
  const [query, setQuery] = useState('');
  const dialog = useRef<HTMLDialogElement>(null);
  const isHome = page === 'overview';
  const activeSection = location.hash.split('/')[2];
  useEffect(() => {
    const listener = (e: KeyboardEvent) => { if ((e.metaKey || e.ctrlKey) && e.key === 'k') { e.preventDefault(); setSearch(v => !v); } };
    window.addEventListener('keydown', listener);
    return () => window.removeEventListener('keydown', listener);
  }, []);
  useEffect(() => { if (search) dialog.current?.showModal(); else dialog.current?.close(); }, [search]);
  useEffect(() => { setMenu(false); setSearch(false); }, [page]);
  const searchItems = [
    ...navigation.map(n => ({ title: n.label, text: n.description, href: `#/${n.id}` })),
    ...pageSections.problem.map(([id, label]) => ({ title: label, text: 'Problem and design', href: `#/problem/${id}` })),
    ...configuration.map(r => ({ title: r[0], text: r[2], href: '#/reference/proxy-configuration' })),
    ...metricFamilies.map(r => ({ title: r[0], text: r[2], href: '#/metrics/metric-families' })),
  ].filter(item => `${item.title} ${item.text}`.toLowerCase().includes(query.toLowerCase()));
  return <div className={`site-app ${isHome ? 'is-home' : 'is-docs'}`}>
    <a className="skip-link" href="#content" onClick={e => { e.preventDefault(); document.getElementById('content')?.focus(); }}>Skip to content</a>
    <header className="site-header"><div className="header-inner">
      <a className="brand" href="#/overview" aria-label="Gatewise home"><img src="logo.svg" alt="" />gatewise</a><span className="header-label">{isHome ? 'HTTP admission control' : 'Documentation'}</span>{isHome && <nav className={`home-header-nav ${menu ? 'open' : ''}`} aria-label="Home navigation">{navigation.filter(n => n.id !== 'overview').map(n => <a key={n.id} href={`#/${n.id}`} onClick={() => setMenu(false)}>{n.id === 'installation' ? 'Installation' : n.label}</a>)}</nav>}
      <div className="header-actions"><button className="search-button" onClick={() => { setQuery(''); setSearch(true); }} aria-label="Search documentation">Search <kbd>⌘ K</kbd></button><a className="header-download" href="downloads/gatewise-demo.zip" download>Download demo example</a><button className="menu-toggle" aria-label="Toggle navigation" aria-expanded={menu} onClick={() => setMenu(v => !v)}>☰</button></div>
    </div></header>
    {isHome ? <><main id="content" tabIndex={-1}>{children}</main><footer className="home-footer"><span>Gatewise</span><a href="#/problem">Design documentation</a><a href="downloads/gatewise-demo.zip" download>Demo example</a></footer></> : <div className="site-shell">
      {menu && <button className="sidebar-backdrop" aria-label="Close navigation" onClick={() => setMenu(false)} />}
      <aside className={`docs-sidebar ${menu ? 'open' : ''}`}><nav aria-label="Main navigation">
        {navigation.map(n => <div key={n.id} className="nav-group"><a href={`#/${n.id}`} aria-current={page === n.id && !activeSection ? 'page' : undefined} className={page === n.id ? 'current-page' : ''} onClick={() => { setMenu(false); if (location.hash === `#/${n.id}`) window.scrollTo({ top: 0, behavior: 'auto' }); }}>{n.label}</a>{page === n.id && <ul>{pageSections[n.id].map(([id, label]) => <li key={id}><a href={`#/${n.id}/${id}`} aria-current={activeSection === id ? 'location' : undefined} onClick={() => { setMenu(false); if (location.hash === `#/${n.id}/${id}`) document.getElementById(id)?.scrollIntoView({ behavior: 'auto' }); }}>{label}</a></li>)}</ul>}</div>)}
      </nav><div className="sidebar-status">Gatewise<br />v0.1.0</div></aside>
      <div className="document-column"><main id="content" tabIndex={-1}>{children}</main><footer><span>Gatewise documentation</span><a href="downloads/gatewise-demo.zip" download>Demo example</a><a href="#/reference">API reference</a></footer></div>
    </div>}
    <dialog ref={dialog} className="search-dialog" onClose={() => setSearch(false)} onClick={e => { if (e.target === e.currentTarget) setSearch(false); }} aria-label="Search documentation"><div className="search-dialog-inner"><div className="search-input-row"><input aria-label="Search documentation" placeholder="Search documentation" value={query} onChange={e => setQuery(e.target.value)} autoFocus /><button onClick={() => setSearch(false)} aria-label="Close search">Esc</button></div><div className="search-results">{searchItems.length ? searchItems.slice(0, 12).map((item, i) => <a key={i} href={item.href} onClick={() => setSearch(false)}><strong>{item.title}</strong><span>{item.text}</span></a>) : <p>No matching results.</p>}</div></div></dialog>
  </div>;
}
