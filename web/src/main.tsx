import { render } from 'preact';
import { App } from './app';
import './styles/base.css';
import './styles/layout.css';
import './styles/components.css';

const root = document.getElementById('root');
if (root) render(<App />, root);
