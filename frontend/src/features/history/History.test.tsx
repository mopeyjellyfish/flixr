import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { History } from './History';
import { strictFetch } from '../../test/http';
afterEach(()=>{cleanup();vi.restoreAllMocks();});
describe('history',()=>{
 it('shows an empty profile history and reports loading failure',async()=>{vi.spyOn(globalThis,'fetch').mockImplementation(strictFetch([{path:'/api/v1/history?limit=25',handle:()=>({json:{events:[]}})}]));render(<History onBrowse={()=>undefined} onExit={()=>undefined}/>);expect(await screen.findByRole('heading',{name:'Viewing history'})).toBeVisible();expect(screen.getByRole('button',{name:'Clear history'})).toBeDisabled();cleanup();vi.spyOn(globalThis,'fetch').mockImplementation(strictFetch([{path:'/api/v1/history?limit=25',handle:()=>({status:500,json:{}})}]));render(<History onBrowse={()=>undefined} onExit={()=>undefined}/>);expect(await screen.findByRole('alert')).toHaveTextContent(/could not load/i);});
 it('clears and restores only the active profile history',async()=>{let cleared=false;vi.spyOn(globalThis,'fetch').mockImplementation(strictFetch([{path:'/api/v1/history?limit=25',handle:()=>({json:{events:cleared?[]:[{id:'one',catalog_id:'gone',title:'Gone',kind:'film',type:'summary',provenance:'import',source_time:null,recorded_at:1}]}})},{path:'/api/v1/history/clear',method:'POST',handle:()=>{cleared=true;return {json:{id:'clear-1',undo_until:Date.now()+300000}};}},{path:'/api/v1/history/clear/clear-1/undo',method:'POST',handle:()=>{cleared=false;return {json:{restored:true}};}}]));render(<History onBrowse={()=>undefined} onExit={()=>undefined}/>);expect(await screen.findByText(/gone/i)).toBeVisible();fireEvent.click(screen.getByRole('button',{name:'Clear history'}));expect(await screen.findByRole('button',{name:'Undo clear'})).toBeVisible();expect(screen.queryByText(/gone/i)).not.toBeInTheDocument();fireEvent.click(screen.getByRole('button',{name:'Undo clear'}));await waitFor(()=>expect(screen.getByText(/gone/i)).toBeVisible());});
});

function deferred<T>() {
 let resolve!: (value: T) => void;
 const promise = new Promise<T>((done) => { resolve = done; });
 return { promise, resolve };
}
const event = (id: string) => ({ id, catalog_id: id, title: id, kind: 'film', type: 'summary', provenance: 'import', source_time: null, recorded_at: 1 });

it('invalidates a pending page when clearing and keeps one usable undo', async () => {
 const page = deferred<{json: unknown}>();
 const clear = deferred<{json: unknown}>();
 vi.spyOn(globalThis, 'fetch').mockImplementation(strictFetch([
  { path: '/api/v1/history?limit=25', handle: () => ({json: {events: [event('First')], next: 'next'}}) },
  { path: '/api/v1/history?limit=25&before=next', handle: () => page.promise },
  { path: '/api/v1/history/clear', method: 'POST', handle: () => clear.promise },
  { path: '/api/v1/history/clear/clear-one/undo', method: 'POST', handle: () => ({json: {restored: true}}) },
 ]));
 render(<History onBrowse={() => undefined} onExit={() => undefined}/>);
 fireEvent.click(await screen.findByRole('button', {name: 'Load more'}));
 fireEvent.click(screen.getByRole('button', {name: 'Clear history'}));
 expect(screen.getByRole('button', {name: 'Clear history'})).toBeDisabled();
 await act(async () => { clear.resolve({json: {id: 'clear-one', undo_until: Date.now()+300000}}); });
 await act(async () => { page.resolve({json: {events: [event('Late')], next: 'older'}}); });
 expect(screen.queryByText('Late')).not.toBeInTheDocument();
 expect(screen.queryByRole('button', {name: 'Load more'})).not.toBeInTheDocument();
 fireEvent.click(screen.getByRole('button', {name: 'Undo clear'}));
 expect(await screen.findByText('First')).toBeVisible();
});

it('does not request the same page twice while it is pending', async () => {
 const page = deferred<{json: unknown}>();
 vi.spyOn(globalThis, 'fetch').mockImplementation(strictFetch([
  {path: '/api/v1/history?limit=25', handle: () => ({json: {events: [event('First')], next: 'next'}})},
  {path: '/api/v1/history?limit=25&before=next', handle: () => page.promise},
 ]));
 render(<History onBrowse={() => undefined} onExit={() => undefined}/>);
 const more = await screen.findByRole('button', {name: 'Load more'});
 fireEvent.click(more); fireEvent.click(more);
 expect(more).toBeDisabled();
 await act(async () => { page.resolve({json: {events: [event('Second')]}}); });
 expect(screen.getAllByText('Second')).toHaveLength(1);
});

it('displays local completion dates and known imported dates without dating unknown imports', async () => {
 const timestamp = Date.UTC(2024, 4, 6, 12);
 vi.spyOn(globalThis, 'fetch').mockImplementation(strictFetch([{path:'/api/v1/history?limit=25',handle:()=>({json:{events:[
  {...event('Local'), type:'completed', provenance:'local', recorded_at:timestamp},
  {...event('Known import'), source_time:timestamp},
  event('Unknown import'),
 ]}})}]));
 render(<History onBrowse={() => undefined} onExit={() => undefined}/>);
 await screen.findByText('Local');
 expect(screen.getAllByText(new Date(timestamp).toLocaleDateString())).toHaveLength(2);
 expect(screen.getAllByText(/Date unknown/)).toHaveLength(1);
});

it('keeps history usable when a previously stored source date is out of range', async () => {
 vi.spyOn(globalThis, 'fetch').mockImplementation(strictFetch([{path:'/api/v1/history?limit=25',handle:()=>({json:{events:[
  {...event('Malformed old import'), source_time:Number('9223372036854775807')},
 ]}})}]));
 render(<History onBrowse={() => undefined} onExit={() => undefined}/>);
 expect(await screen.findByText('Malformed old import')).toBeVisible();
 expect(screen.getByText('Date unavailable')).toBeVisible();
 expect(screen.getByRole('button', {name:'Clear history'})).toBeEnabled();
});

it('expires undo at the returned deadline', async () => {
 vi.useFakeTimers();
 try {
  vi.spyOn(globalThis,'fetch').mockImplementation(strictFetch([
   {path:'/api/v1/history?limit=25',handle:()=>({json:{events:[event('First')]}})},
   {path:'/api/v1/history/clear',method:'POST',handle:()=>({json:{id:'clear-one',undo_until:Date.now()+300000}})},
  ]));
  render(<History onBrowse={()=>undefined} onExit={()=>undefined}/>);
  await act(async()=>{});
  await act(async()=>{fireEvent.click(screen.getByRole('button',{name:'Clear history'}));});
  expect(screen.getByRole('button',{name:'Undo clear'})).toBeVisible();
  await act(async()=>{await vi.advanceTimersByTimeAsync(300001);});
  expect(screen.queryByRole('button',{name:'Undo clear'})).not.toBeInTheDocument();
  expect(screen.getByRole('button',{name:'Clear history'})).toBeVisible();
 } finally {vi.useRealTimers();}
});

it('releases an expired undo after the server rejects it so new history can be cleared', async () => {
 let loads=0;
 vi.spyOn(globalThis,'fetch').mockImplementation(strictFetch([
  {path:'/api/v1/history?limit=25',handle:()=>({json:{events:[event(loads++?'New viewing':'First')]}})},
  {path:'/api/v1/history/clear',method:'POST',handle:()=>({json:{id:'clear-one',undo_until:Date.now()+300000}})},
  {path:'/api/v1/history/clear/clear-one/undo',method:'POST',handle:()=>({status:404,json:{error:{code:'history_clear_not_found'}}})},
 ]));
 render(<History onBrowse={()=>undefined} onExit={()=>undefined}/>);
 await screen.findByText('First');
 fireEvent.click(screen.getByRole('button',{name:'Clear history'}));
 fireEvent.click(await screen.findByRole('button',{name:'Undo clear'}));
 fireEvent.click(await screen.findByRole('button',{name:'Reload history'}));
 expect(await screen.findByText('New viewing')).toBeVisible();
 expect(screen.queryByRole('button',{name:'Undo clear'})).not.toBeInTheDocument();
 expect(screen.getByRole('button',{name:'Clear history'})).toBeEnabled();
});

it('keeps undo retryable after a transient server failure', async () => {
 let attempts=0;
 vi.spyOn(globalThis,'fetch').mockImplementation(strictFetch([
  {path:'/api/v1/history?limit=25',handle:()=>({json:{events:[event('First')]}})},
  {path:'/api/v1/history/clear',method:'POST',handle:()=>({json:{id:'clear-one',undo_until:Date.now()+300000}})},
  {path:'/api/v1/history/clear/clear-one/undo',method:'POST',handle:()=>attempts++?{json:{restored:true}}:{status:500,json:{error:{code:'request_failed'}}}},
 ]));
 render(<History onBrowse={()=>undefined} onExit={()=>undefined}/>);
 await screen.findByText('First');
 fireEvent.click(screen.getByRole('button',{name:'Clear history'}));
 fireEvent.click(await screen.findByRole('button',{name:'Undo clear'}));
 await screen.findByRole('alert');
 fireEvent.click(screen.getByRole('button',{name:'Undo clear'}));
 expect(await screen.findByText('First')).toBeVisible();
 expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});
