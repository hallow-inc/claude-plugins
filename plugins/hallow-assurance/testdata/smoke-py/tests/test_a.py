from hypothesis import given, strategies as st

from smoke.a import double


@given(st.integers())
def test_double_equals_adding_a_number_to_itself(n):
    assert double(n) == n + n


@given(st.integers(), st.integers())
def test_double_distributes_over_addition(a, b):
    assert double(a + b) == double(a) + double(b)
