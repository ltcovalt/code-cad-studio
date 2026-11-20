import { mount } from 'svelte';
import 'three-cad-viewer/css';
import './styles.css';
import App from './App.svelte';

const target = document.getElementById('app');
if (!target) {
  throw new Error('Root #app element not found');
}

mount(App, { target });
