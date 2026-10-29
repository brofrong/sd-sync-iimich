import { mount } from "svelte";
import "./styles/theme.css";
import App from "./App.svelte";

// Freeze CSS animations while the window is hidden or minimised.
const syncPaused = () =>
  document.body.classList.toggle("paused", document.hidden);
document.addEventListener("visibilitychange", syncPaused);
syncPaused();

mount(App, { target: document.getElementById("app")! });
