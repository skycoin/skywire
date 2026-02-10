import { build, context } from 'esbuild';
import { copyFileSync, mkdirSync } from 'fs';
import { resolve, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const outdir = resolve(__dirname, '..', 'legacy');
const isWatch = process.argv.includes('--watch');

mkdirSync(outdir, { recursive: true });

const buildOptions = {
  entryPoints: [resolve(__dirname, 'src', 'index.ts')],
  bundle: true,
  format: 'iife',
  target: 'es2020',
  minify: !isWatch,
  sourcemap: isWatch,
  outfile: resolve(outdir, 'bundle.js'),
  logLevel: 'info',
};

// Copy index.html to legacy/
copyFileSync(
  resolve(__dirname, 'index.html'),
  resolve(outdir, 'index.html')
);

if (isWatch) {
  const ctx = await context(buildOptions);
  await ctx.watch();
  console.log('Watching for changes...');
} else {
  await build(buildOptions);
  console.log('Build complete.');
}
