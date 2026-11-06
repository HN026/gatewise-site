import { lazy, Suspense, useEffect, useState } from 'react';
import Layout from './components/Layout';
import { navigation, type PageId } from './data/content';
import Overview from './pages/Overview';
import Problem from './pages/Problem';
import Installation from './pages/Installation';
import Examples from './pages/Examples';
const Benchmarks=lazy(()=>import('./pages/Benchmarks'));
import Metrics from './pages/Metrics';
import Reference from './pages/Reference';
import Versions from './pages/Versions';

function resolvePage(route: string): PageId {
  const value = route.split('/')[0];
  if (value === 'overview' && route.includes('/')) return 'problem';
  return navigation.some(n => n.id === value) ? value as PageId : 'overview';
}
export default function App() {
  const [route, setRoute] = useState(() => location.hash.replace(/^#\//, '') || 'overview');
  const page = resolvePage(route);
  useEffect(() => { const update = () => setRoute(location.hash.replace(/^#\//, '') || 'overview'); window.addEventListener('hashchange', update); return () => window.removeEventListener('hashchange', update); }, []);
  useEffect(() => {
    document.title = `Gatewise — ${navigation.find(n => n.id === page)!.label}`;
    const frame = requestAnimationFrame(() => {
      const section = document.getElementById(route.split('/')[1] ?? '');
      if (section) section.scrollIntoView({ behavior: 'auto' });
      else window.scrollTo({ top: 0, behavior: 'auto' });
    });
    return () => cancelAnimationFrame(frame);
  }, [route, page]);
  const pages = { overview: <Overview />, problem: <Problem />, installation: <Installation />, examples: <Examples />, benchmarks: <Benchmarks />, metrics: <Metrics />, reference: <Reference />, versions: <Versions /> };
  return <Layout page={page}><Suspense fallback={<div className="container"><p>Loading benchmark results…</p></div>}>{pages[page]}</Suspense></Layout>;
}
