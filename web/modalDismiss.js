// modalDismiss.js — 단순 폼 모달의 ESC + 백드롭 클릭 닫기 패턴.
//
// 모달이 ESC/백드롭만 처리하는 단순 케이스용. 라이트박스(화살표·Delete
// 추가 키) 나 settings(Enter 제출 추가) 처럼 keydown 분기가 더 있는
// 모달은 이 헬퍼 대상이 아니다 — 직접 listener 를 단다.

export function wireModalDismiss(modal, close) {
  modal.addEventListener('click', e => {
    if (e.target === modal) close();
  });
  document.addEventListener('keydown', e => {
    if (modal.classList.contains('hidden')) return;
    if (e.key === 'Escape') close();
  });
}
