// The download page: open the tab for this computer, switch tabs, copy lines.
const tabs = [...document.querySelectorAll("[role=tab]")];

function show(id) {
  for (const t of tabs) {
    const on = t.getAttribute("aria-controls") === id;
    t.setAttribute("aria-selected", on);
    document.getElementById(t.getAttribute("aria-controls")).hidden = !on;
  }
}

for (const t of tabs) t.addEventListener("click", () => show(t.getAttribute("aria-controls")));

const ua = navigator.userAgent;
if (/Mac OS X|Macintosh/.test(ua)) show("mac");
else if (/Linux/.test(ua) && !/Android/.test(ua)) show("linux");

for (const b of document.querySelectorAll(".copy")) {
  b.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(b.previousElementSibling.textContent);
      b.textContent = "Copied";
    } catch (_) {
      b.textContent = "Select and copy";
    }
    setTimeout(() => { b.textContent = "Copy"; }, 2000);
  });
}
