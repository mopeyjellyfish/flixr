import { useState } from 'react';
import { Wordmark } from '../../modules/productChrome/Wordmark';
import { LoadingButton } from '../../vendor/interior/loading-button';
import { CopyButton } from '../../vendor/interior/copy-button';
import { ProgressBar } from '../../vendor/interior/progress-bar';
import { Tabs } from '../../vendor/interior/tabs';
import { SegmentedControl } from '../../vendor/interior/segmented-control';
import { Accordion } from '../../vendor/interior/accordion';
import { OtpInput } from '../../vendor/interior/otp-input';
import { Modal } from '../../vendor/interior/modal';
import { ExpandingSearch } from '../../vendor/interior/expanding-search';
import { TextReveal } from '../../vendor/interior/text-reveal';
import { SkeletonSwap } from '../../vendor/interior/skeleton-swap';
import { CollapsibleBanner } from '../../vendor/interior/collapsible-banner';

const inventory = Object.keys(import.meta.glob('../../vendor/interior/*.tsx')).map((path) => path.split('/').pop()!.replace('.tsx', ''));
export default function InteriorGallery() {
  const [modal, setModal] = useState(false);
  const [progress, setProgress] = useState(35);
  const [ready, setReady] = useState(true);
  const [search, setSearch] = useState('');
  return <main className="interior-gallery dark"><header><Wordmark /><a className="button-link" href="/home">← Back to Flixr</a></header><p className="eyebrow">DEVELOPMENT PLAYGROUND</p><h1><TextReveal text="The details make the difference." /></h1><p>54 vendored components. Try the interactions below; these controls only change this playground.</p><div className="gallery-grid">
    <section><h2>Action feedback</h2><LoadingButton onAction={() => new Promise((resolve) => setTimeout(resolve, 900))} pendingLabel="Saving…" successLabel="Saved">Save preferences</LoadingButton><CopyButton value="http://localhost:19879" label="Copy demo link" /></section>
    <section><h2>View switcher</h2><SegmentedControl label="Preview layout" options={[{ value: 'rows', label: 'Rows' }, { value: 'grid', label: 'Grid' }]} /></section>
    <section><h2>Tabs</h2><Tabs items={[{ value: 'library', label: 'Library' }, { value: 'household', label: 'Household' }, { value: 'screens', label: 'Screens' }]} renderPanel={(value) => <p className="p-4">{value} settings preview</p>} /></section>
    <section><h2>Progress</h2><ProgressBar value={progress} label="Sample scan" /><button onClick={() => setProgress((value) => value >= 100 ? 0 : value + 5)}>Advance scan</button></section>
    <section><h2>Search</h2><ExpandingSearch value={search} onChange={setSearch} label="Search components" /><p aria-live="polite">{inventory.filter((name) => name.includes(search.toLowerCase())).length} components</p></section>
    <section><h2>PIN interaction</h2><OtpInput length={4} label="Sample PIN" hint="Playground only. Nothing is submitted." /></section>
    <section><h2>Loading without jumps</h2><SkeletonSwap ready={ready} reserve={90} skeleton={<div className="gallery-placeholder" />}><p>Your local library is ready.</p></SkeletonSwap><button onClick={() => setReady(!ready)}>Toggle loading</button></section>
    <section><h2>Disclosure</h2><Accordion items={[{ id: 'local', title: 'Local first', content: 'The demo uses cached artwork and metadata from your server.' }, { id: 'media', title: 'Media preview', content: 'Media files are not included in this demo.' }]} /></section>
    <section><h2>Modal</h2><button onClick={() => setModal(true)}>Open modal</button><Modal open={modal} onClose={() => setModal(false)} title="Make yourself at home" description="An Interior dialog with focus management and keyboard dismissal."><p>Press Escape, click outside, or use the close button.</p></Modal></section>
    <section><h2>Collapsible notice</h2><CollapsibleBanner title="Your demo is ready" description="50 films and 50 shows, served locally." defaultState="open" /></section>
  </div><section className="component-inventory"><h2>Full vendored collection</h2><ul>{inventory.filter((name) => name.includes(search.toLowerCase())).map((name) => <li key={name}>{name}</li>)}</ul><p>Interior · MIT · <a href="https://github.com/ddoemonn/interior">Upstream source</a></p></section></main>;
}
