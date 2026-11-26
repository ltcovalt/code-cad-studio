import { describe, expect, it } from 'vitest';
import {
  normalizePathKey,
  pathBasename,
  pathDirname,
  pathParent,
  pathsEqual,
  revealActionLabel,
  suggestLibraryName,
} from './paths';

describe('normalizePathKey', () => {
  it('returns empty string for blank inputs', () => {
    expect(normalizePathKey('')).toBe('');
    expect(normalizePathKey('   \t\n  ')).toBe('');
  });

  it('normalizes slashes, trims whitespace, and strips trailing slashes on POSIX', () => {
    expect(normalizePathKey('  /home/luke/Repos/code-cad/  ', 'linux')).toBe(
      '/home/luke/Repos/code-cad'
    );
    expect(normalizePathKey('/home/luke//Repos///code-cad///', 'darwin')).toBe(
      '/home/luke/Repos/code-cad'
    );
  });

  it('preserves POSIX root slash', () => {
    expect(normalizePathKey('/', 'linux')).toBe('/');
    expect(normalizePathKey('///', 'linux')).toBe('/');
  });

  it('preserves case on POSIX', () => {
    expect(normalizePathKey('/home/luke/Repos/code-cad', 'linux')).toBe(
      '/home/luke/Repos/code-cad'
    );
    expect(normalizePathKey('/home/luke/repos/code-cad', 'linux')).toBe(
      '/home/luke/repos/code-cad'
    );
  });

  it('lowercases and normalizes Windows drive paths even when os is omitted', () => {
    expect(normalizePathKey('C:\\Users\\luke\\Repos\\code-cad\\')).toBe(
      'c:/users/luke/repos/code-cad'
    );
    expect(normalizePathKey('c:/Users/luke/repos/code-cad')).toBe(
      'c:/users/luke/repos/code-cad'
    );
  });

  it('preserves Windows drive root slash', () => {
    expect(normalizePathKey('C:\\')).toBe('c:/');
    expect(normalizePathKey('c:/')).toBe('c:/');
    expect(normalizePathKey('D:\\\\\\', 'windows')).toBe('d:/');
  });

  it('normalizes UNC paths and lowercases them', () => {
    expect(normalizePathKey('\\\\NAS\\CAD\\Models\\')).toBe('//nas/cad/models');
    expect(normalizePathKey('//nas/cad//models/', 'windows')).toBe('//nas/cad/models');
  });

  it('lowercases relative paths when os is windows', () => {
    expect(normalizePathKey('Models\\Brackets\\', 'windows')).toBe('models/brackets');
    expect(normalizePathKey('Models/Brackets/', 'linux')).toBe('Models/Brackets');
  });
});

describe('pathsEqual', () => {
  it('matches Windows paths with differing case, slashes, and trailing separators', () => {
    expect(
      pathsEqual('C:\\Users\\luke\\Repos\\code-cad\\', 'c:/users/luke/repos/code-cad', 'windows')
    ).toBe(true);
    expect(
      pathsEqual('C:\\Users\\luke\\Repos\\code-cad', 'C:\\Users\\luke\\repos\\code-cad')
    ).toBe(true);
    expect(pathsEqual('\\\\Server\\Share\\Lib\\', '\\\\server\\share\\lib')).toBe(true);
  });

  it('stays case-sensitive on Linux and macOS for POSIX paths', () => {
    expect(pathsEqual('/home/luke/Repos/code-cad', '/home/luke/repos/code-cad', 'linux')).toBe(
      false
    );
    expect(pathsEqual('/home/luke/Repos/code-cad/', '/home/luke/Repos/code-cad', 'linux')).toBe(
      true
    );
  });

  it('returns false for empty or whitespace paths', () => {
    expect(pathsEqual('', '')).toBe(false);
    expect(pathsEqual('   ', '   ', 'windows')).toBe(false);
    expect(pathsEqual('/foo', '', 'linux')).toBe(false);
  });
});

describe('pathBasename', () => {
  it('extracts basename for POSIX and Windows paths', () => {
    expect(pathBasename('/home/luke/repos/code-cad')).toBe('code-cad');
    expect(pathBasename('/home/luke/repos/code-cad/')).toBe('code-cad');
    expect(pathBasename('C:\\Users\\luke\\Repos\\code-cad\\')).toBe('code-cad');
    expect(pathBasename('demo')).toBe('demo');
  });

  it('returns empty string for roots and empty strings', () => {
    expect(pathBasename('')).toBe('');
    expect(pathBasename('/')).toBe('');
    expect(pathBasename('C:\\')).toBe('');
    expect(pathBasename('C:/')).toBe('');
  });
});

describe('pathParent', () => {
  it('extracts parent folder segment for POSIX and Windows paths', () => {
    expect(pathParent('/home/luke/Repos/code-cad')).toBe('Repos');
    expect(pathParent('C:\\Users\\luke\\Documents\\code-cad-studio\\demo\\')).toBe(
      'code-cad-studio'
    );
  });

  it('returns empty string when there is no parent segment', () => {
    expect(pathParent('')).toBe('');
    expect(pathParent('/')).toBe('');
    expect(pathParent('/code-cad')).toBe('');
    expect(pathParent('C:\\')).toBe('');
  });
});

describe('suggestLibraryName', () => {
  it('uses the folder basename when unique', () => {
    expect(suggestLibraryName('/home/luke/Repos/code-cad', [])).toBe('code-cad');
    expect(suggestLibraryName('C:\\Users\\luke\\Documents\\code-cad-studio\\demo', ['code-cad'])).toBe(
      'demo'
    );
  });

  it('disambiguates colliding names case-insensitively with parent folder', () => {
    expect(
      suggestLibraryName('C:\\Users\\luke\\Documents\\code-cad-studio\\code-cad', ['Code-CAD'])
    ).toBe('code-cad (code-cad-studio)');
  });

  it('falls back to numeric suffix when parent disambiguation also collides', () => {
    expect(
      suggestLibraryName('/tmp/work/code-cad', ['code-cad', 'code-cad (work)'])
    ).toBe('code-cad (work) 2');
    expect(suggestLibraryName('/code-cad', ['code-cad'])).toBe('code-cad (2)');
  });

  it('falls back to Library when path has no basename', () => {
    expect(suggestLibraryName('/', [])).toBe('Library');
    expect(suggestLibraryName('C:\\', ['Library'])).toBe('Library (2)');
  });
});

describe('pathDirname', () => {
  it('returns the parent directory path for POSIX and Windows paths', () => {
    expect(pathDirname('/home/luke/repos/code-cad')).toBe('/home/luke/repos');
    expect(pathDirname('/home/luke/repos/code-cad/')).toBe('/home/luke/repos');
    expect(pathDirname('C:\\Users\\luke\\Documents\\code-cad')).toBe('C:\\Users\\luke\\Documents');
    expect(pathDirname('C:\\code-cad')).toBe('C:\\');
    expect(pathDirname('/code-cad')).toBe('/');
    expect(pathDirname('/')).toBe('');
    expect(pathDirname('C:\\')).toBe('');
    expect(pathDirname('')).toBe('');
  });
});

describe('revealActionLabel', () => {
  it('returns OS-native file manager labels', () => {
    expect(revealActionLabel('windows')).toBe('Reveal in Explorer');
    expect(revealActionLabel('Windows')).toBe('Reveal in Explorer');
    expect(revealActionLabel('darwin')).toBe('Reveal in Finder');
    expect(revealActionLabel('linux')).toBe('Reveal in File Manager');
    expect(revealActionLabel(undefined)).toBe('Reveal in File Manager');
    expect(revealActionLabel('')).toBe('Reveal in File Manager');
  });
});
