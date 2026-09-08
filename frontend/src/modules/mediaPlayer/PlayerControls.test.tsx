import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { PlayerControls } from './PlayerControls';
afterEach(cleanup);
it('previews a scrub without seeking until release and exposes a single transport toggle', () => {
  const seek = vi.fn(); const toggle = vi.fn();
  render(<PlayerControls playing durationMS={120000} positionMS={10000} onSeek={seek} onToggle={toggle} video={{ current: null }} stage={{ current: null }} />);
  const timeline = screen.getByRole('slider', { name: 'Seek' });
  fireEvent.pointerDown(timeline);
  fireEvent.change(timeline, { target: { value: '60000' } });
  expect(seek).not.toHaveBeenCalled();
  fireEvent.pointerUp(timeline);
  expect(seek).toHaveBeenCalledWith(60000);
  fireEvent.click(screen.getByRole('button', { name: 'Pause' }));
  expect(toggle).toHaveBeenCalledOnce();
});
it('provides bounded ten-second seek steps and source-time chapter navigation', () => {
  const seek = vi.fn();
  render(<PlayerControls playing={false} durationMS={120000} positionMS={3000} onSeek={seek} onToggle={() => {}} video={{ current: null }} stage={{ current: null }} chapters={[{ title: 'Opening', start_ms: 0, end_ms: 20000 }, { title: 'Arrival', start_ms: 20000, end_ms: 120000 }]} />);
  fireEvent.click(screen.getByRole('button', { name: 'Back 10 seconds' }));
  expect(seek).toHaveBeenLastCalledWith(0);
  fireEvent.click(screen.getByText('Chapters'));
  fireEvent.click(screen.getByRole('button', { name: /Arrival/ }));
  expect(seek).toHaveBeenLastCalledWith(20000);
});
it('closes settings from a focused field with Escape and returns focus to the menu', () => {
  const root = document.createElement('main'); document.body.append(root);
  render(<PlayerControls playing durationMS={120000} positionMS={0} onSeek={() => {}} onToggle={() => {}} video={{current:null}} stage={{current:root}} />, {container: root});
  fireEvent.click(screen.getByText('Settings'));
  const speed = screen.getByRole('combobox', {name:'Playback speed'});
  speed.focus(); fireEvent.keyDown(speed,{key:'Escape'});
  expect(speed).not.toBeVisible();
  expect(screen.getByText('Settings')).toHaveFocus();
});
it('reports rejected fullscreen without interrupting playback', async () => {
  const root = document.createElement('main');
  root.requestFullscreen=vi.fn().mockRejectedValue(new Error('unsupported'));
  const toggle=vi.fn();
  render(<PlayerControls playing durationMS={120000} positionMS={0} onSeek={() => {}} onToggle={toggle} video={{current:null}} stage={{current:root}} />);
  fireEvent.click(screen.getByRole('button',{name:'Fullscreen'}));
  expect(await screen.findByRole('status')).toHaveTextContent('keep watching');
  expect(toggle).not.toHaveBeenCalled();
});
it('accumulates rapid seek steps before the media reports a new position', () => {
  const seek = vi.fn();
  render(<PlayerControls playing durationMS={120000} positionMS={10000} onSeek={seek} onToggle={() => {}} video={{current:null}} stage={{current:null}} />);
  fireEvent.click(screen.getByRole('button', {name:'Forward 10 seconds'}));
  fireEvent.click(screen.getByRole('button', {name:'Forward 10 seconds'}));
  fireEvent.click(screen.getByRole('button', {name:'Back 10 seconds'}));
  expect(seek.mock.calls.map(([value]) => value)).toEqual([20000,30000,20000]);
});
it('accumulates discrete keyboard seeks and ignores held-key repeats', () => {
  const seek = vi.fn(); const root=document.createElement('main'); document.body.append(root);
  render(<PlayerControls playing durationMS={120000} positionMS={10000} onSeek={seek} onToggle={() => {}} video={{current:null}} stage={{current:root}} />, {container:root});
  fireEvent.keyDown(root,{key:'ArrowRight'});
  fireEvent.keyDown(root,{key:'ArrowRight',repeat:true});
  fireEvent.keyDown(root,{key:'ArrowRight'});
  expect(seek.mock.calls.map(([value]) => value)).toEqual([20000,30000]);
});
it('retains the latest seek intent while an earlier seek reports its position', () => {
  const seek = vi.fn(); const video={current:null}; const stage={current:null}; const toggle=()=>{};
  const {rerender}=render(<PlayerControls playing seeking durationMS={120000} positionMS={10000} onSeek={seek} onToggle={toggle} video={video} stage={stage} />);
  fireEvent.click(screen.getByRole('button',{name:'Forward 10 seconds'}));
  fireEvent.click(screen.getByRole('button',{name:'Forward 10 seconds'}));
  rerender(<PlayerControls playing seeking durationMS={120000} positionMS={20000} onSeek={seek} onToggle={toggle} video={video} stage={stage} />);
  fireEvent.click(screen.getByRole('button',{name:'Forward 10 seconds'}));
  expect(seek).toHaveBeenLastCalledWith(40000);
  rerender(<PlayerControls playing durationMS={120000} positionMS={41000} onSeek={seek} onToggle={toggle} video={video} stage={stage} />);
  fireEvent.click(screen.getByRole('button',{name:'Back 10 seconds'}));
  expect(seek).toHaveBeenLastCalledWith(31000);
});
