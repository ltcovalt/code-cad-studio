import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ApiRequestError,
  cancelRender,
  cancelSetupTask,
  exportModel,
  getModel,
  openInEditor,
  openInSlicer,
  revealFile,
  scaffoldModel,
  subscribeEvents,
  updateModelDefaults,
  windowClose,
  windowMinimize,
  windowToggleMaximize,
} from './api';

describe('api client', () => {
  const originalFetch = globalThis.fetch;
  const originalEventSource = globalThis.EventSource;

  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    globalThis.fetch = originalFetch;
    globalThis.EventSource = originalEventSource;
  });

  it('sends Content-Type: application/json and body "{}" on empty POST requests', async () => {
    const fetchMock = vi.fn(async () => new Response('', { status: 204 }));
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    await cancelSetupTask();
    await windowMinimize();
    await windowToggleMaximize();
    await windowClose();
    await cancelRender();

    expect(fetchMock).toHaveBeenCalledTimes(5);
    for (const call of fetchMock.mock.calls as unknown as Array<[string, RequestInit]>) {
      const [_url, init] = call;
      expect(init.method).toBe('POST');
      expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json');
      expect(init.body).toBe('{}');
    }
  });

  it('routes windowMinimize and windowClose via fetchJSON and propagates ApiRequestError code & traceback', async () => {
    globalThis.fetch = vi.fn(async () =>
      new Response(
        JSON.stringify({
          ok: false,
          error: 'Build exploded',
          code: 'BUILD_ERR',
          traceback: 'Traceback (most recent call last)...',
        }),
        { status: 500 }
      )
    ) as unknown as typeof fetch;

    let caught: unknown;
    try {
      await windowMinimize();
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(ApiRequestError);
    expect((caught as ApiRequestError).status).toBe(500);
    expect((caught as ApiRequestError).code).toBe('BUILD_ERR');
    expect((caught as ApiRequestError).traceback).toBe('Traceback (most recent call last)...');
  });

  it('URL-encodes canonical modelIDs cleanly and supports desktop/scaffold endpoints', async () => {
    const fetchMock = vi.fn(async () =>
      new Response(JSON.stringify({ ok: true }), { status: 200 })
    );
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    await getModel('code-cad/enclosures/lid');
    expect((fetchMock.mock.calls[0] as unknown as [string])[0]).toBe(
      '/api/models/code-cad%2Fenclosures%2Flid'
    );

    await scaffoldModel({ mode: 'demo' });
    await openInEditor('/tmp/a.py');
    await revealFile('/tmp/a.stl');
    await openInSlicer('/tmp/a.stl');
    await updateModelDefaults('code-cad/enclosures/lid', {
      params: { w: 10 },
      default_preset: { label: 'Lid Base', description: 'Standard lid' },
      dry_run: true,
    });
    expect((fetchMock.mock.calls[1] as unknown as [string])[0]).toBe('/api/models/scaffold');
    expect((fetchMock.mock.calls[2] as unknown as [string])[0]).toBe('/api/desktop/open-editor');
    expect((fetchMock.mock.calls[3] as unknown as [string])[0]).toBe('/api/desktop/reveal-file');
    expect((fetchMock.mock.calls[4] as unknown as [string])[0]).toBe('/api/desktop/open-slicer');
    expect((fetchMock.mock.calls[5] as unknown as [string])[0]).toBe(
      '/api/models/code-cad%2Fenclosures%2Flid/update-defaults'
    );
  });

  it('browser exportModel delays URL.revokeObjectURL and parses JSON error envelopes', async () => {
    const createObjectURL = vi.fn(() => 'blob:mock-url');
    const revokeObjectURL = vi.fn();
    vi.stubGlobal('URL', { ...URL, createObjectURL, revokeObjectURL });

    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});

    globalThis.fetch = vi.fn(async () =>
      new Response(new Blob(['solid mesh']), {
        status: 200,
        headers: { 'Content-Disposition': 'attachment; filename="box_10mm.stl"' },
      })
    ) as unknown as typeof fetch;

    const okRes = await exportModel('lib/box', { w: 10 }, 'stl', false, {
      quality: 'fine',
      filename: 'box_10mm',
    });
    expect(okRes).toEqual({ ok: true, cancelled: false, path: 'box_10mm.stl', size: 10 });
    expect(clickSpy).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).not.toHaveBeenCalled();

    vi.advanceTimersByTime(60_000);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock-url');

    globalThis.fetch = vi.fn(async () =>
      new Response(
        JSON.stringify({ ok: false, error: 'Invalid deflection', code: 'BAD_INPUT', traceback: 'tb' }),
        { status: 400 }
      )
    ) as unknown as typeof fetch;

    const errRes = await exportModel('lib/box', { w: 10 }, 'stl', false);
    expect(errRes).toEqual({
      ok: false,
      error: 'Invalid deflection',
      code: 'BAD_INPUT',
      traceback: 'tb',
    });
  });

  it('subscribeEvents fires onOpen once initially and onReconnect after disconnect/retry', () => {
    const instances: FakeEventSource[] = [];
    class FakeEventSource {
      listeners = new Map<string, Array<(evt: Event) => void>>();
      onopen: (() => void) | null = null;
      onerror: (() => void) | null = null;
      closed = false;
      constructor(public url: string) {
        instances.push(this);
      }
      addEventListener(name: string, fn: (evt: Event) => void) {
        const list = this.listeners.get(name) ?? [];
        list.push(fn);
        this.listeners.set(name, list);
      }
      emit(name: string, data: unknown) {
        const evt = { data: JSON.stringify(data) } as MessageEvent;
        for (const fn of this.listeners.get(name) ?? []) {
          fn(evt);
        }
      }
      close() {
        this.closed = true;
      }
    }

    globalThis.EventSource = FakeEventSource as unknown as typeof EventSource;

    const onOpen = vi.fn();
    const onReconnect = vi.fn();
    const onFileChanged = vi.fn();

    const unsub = subscribeEvents({
      onFileChanged,
      onPresetsChanged: vi.fn(),
      onWorkerError: vi.fn(),
      onOpen,
      onReconnect,
    });

    expect(instances).toHaveLength(1);
    instances[0].onopen?.();
    instances[0].emit('connected', {});
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onReconnect).not.toHaveBeenCalled();

    instances[0].emit('file-changed', { path: '/lib/box.py' });
    expect(onFileChanged).toHaveBeenCalledWith('/lib/box.py');

    // Simulate drop & reconnect
    instances[0].onerror?.();
    expect(instances[0].closed).toBe(true);
    vi.advanceTimersByTime(3000);
    expect(instances).toHaveLength(2);
    instances[1].onopen?.();
    expect(onOpen).toHaveBeenCalledTimes(2);
    expect(onReconnect).toHaveBeenCalledTimes(1);

    unsub();
    expect(instances[1].closed).toBe(true);
  });
});
