import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { BackupPanel } from './BackupPanel';

const policy={enabled:false,destination:'/backups/flixr',schedule_kind:'interval',interval_seconds:86400,local_time:'03:00',timezone:'UTC',retain_count:7,retain_age_seconds:2592000,budget_bytes:10737418240,last_status:'succeeded',last_verified_at:1700000000,next_run_at:1700100000};

beforeEach(()=>vi.restoreAllMocks());
afterEach(cleanup);

test('shows verified backup status and saves scheduled retention policy',async()=>{
  const fetcher=vi.spyOn(globalThis,'fetch').mockImplementation(async(_input,init)=>init?.method==='PUT'?new Response(JSON.stringify({policy:{...policy,enabled:true}})):new Response(JSON.stringify({policy,jobs:[{id:'job-1',trigger:'manual',status:'succeeded',queued_at:1699999900,finished_at:1700000000}]})));
  render(<BackupPanel />);
  expect(await screen.findByDisplayValue('/backups/flixr')).toBeVisible();expect(screen.getByText(/last result:/i).closest('p')).toHaveTextContent('succeeded');expect(screen.getByText(/manual backup/i)).toBeVisible();
  fireEvent.click(screen.getByRole('checkbox',{name:/automatically/i}));fireEvent.click(screen.getByRole('button',{name:/save backup policy/i}));
  await vi.waitFor(()=>expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/backups/policy',expect.objectContaining({method:'PUT'})));
});

test('queues and cancels active work',async()=>{
  const fetcher=vi.spyOn(globalThis,'fetch').mockImplementation(async(_input,init)=>{if(init?.method==='POST')return new Response(JSON.stringify({job:{id:'new',trigger:'manual',status:'queued',queued_at:1}}),{status:202});if(init?.method==='DELETE')return new Response(JSON.stringify({job:{id:'new',trigger:'manual',status:'cancelled',queued_at:1,finished_at:2}}));return new Response(JSON.stringify({policy,jobs:[]}))});
  render(<BackupPanel />);await screen.findByDisplayValue('/backups/flixr');fireEvent.click(screen.getByRole('button',{name:/back up now/i}));expect(await screen.findByText(/manual backup/i)).toBeVisible();fireEvent.click(screen.getByRole('button',{name:/cancel/i}));await vi.waitFor(()=>expect(fetcher).toHaveBeenCalledWith('/api/v1/owner/backups/jobs/new',expect.objectContaining({method:'DELETE'})));
});
